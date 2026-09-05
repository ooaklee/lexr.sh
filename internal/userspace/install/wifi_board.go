package install

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

const (
	// wifiFirmwareDirectory is the distribution-owned WCN7850 firmware location.
	wifiFirmwareDirectory = "lib/firmware/ath12k/WCN7850/hw2.0/"
	// wifiSurfaceBoard is the native Surface Pro 11 board database selector.
	wifiSurfaceBoard = "bus=pci,vendor=17cb,device=1107,subsystem-vendor=17cb,subsystem-device=1107,qmi-chip-id=2,qmi-board-id=255"
	// wifiFallbackBoard is the exact entry qualified by OE's SP11 board fixup.
	wifiFallbackBoard = "bus=pci,vendor=17cb,device=1107,subsystem-vendor=17cb,subsystem-device=3378,qmi-chip-id=2,qmi-board-id=255"
	// maximumWiFiDatabaseBytes bounds both compressed input and expanded data.
	maximumWiFiDatabaseBytes = 16 << 20
	// maximumWiFiBoardBytes bounds one selected calibration payload.
	maximumWiFiBoardBytes = 256 << 10
)

// wifiBoardSelection retains the exact selector and its derived payload.
type wifiBoardSelection struct {
	name string
	data []byte
}

// wifiTLV walks the little-endian type/length records and four-byte alignment
// used by the ath12k API-2 board format. Lengths never escape their container.
func wifiTLV(data []byte, visit func(uint32, []byte) error) error {
	for len(data) > 0 {
		if len(data) < 8 {
			return errors.New("truncated ath12k board record header")
		}
		kind := binary.LittleEndian.Uint32(data[:4])
		size := uint64(binary.LittleEndian.Uint32(data[4:8]))
		padded := (size + 3) &^ uint64(3)
		if padded > uint64(len(data)-8) {
			return errors.New("ath12k board record exceeds its container")
		}
		if err := visit(kind, data[8:8+int(size)]); err != nil {
			return err
		}
		data = data[8+int(padded):]
	}
	return nil
}

// selectWiFiBoard derives only a native Surface entry or the single historical
// OE fallback. It rejects ambiguous matches rather than selecting by order.
func selectWiFiBoard(data []byte) (wifiBoardSelection, error) {
	if len(data) < 20 || len(data) > maximumWiFiDatabaseBytes || !bytes.HasPrefix(data, []byte("QCA-ATH12K-BOARD\x00")) {
		return wifiBoardSelection{}, errors.New("invalid or oversized ath12k API-2 board database")
	}
	selections := map[string][]byte{}
	err := wifiTLV(data[20:], func(kind uint32, record []byte) error {
		if kind != 0 { // Regulatory records do not contain board calibration data.
			return nil
		}
		matches := map[string]bool{}
		var payload []byte
		if err := wifiTLV(record, func(field uint32, value []byte) error {
			switch field {
			case 0:
				name := string(value)
				if name == wifiSurfaceBoard || name == wifiFallbackBoard {
					if matches[name] {
						return errors.New("duplicate SP11 board selector")
					}
					matches[name] = true
				}
			case 1:
				if payload != nil || len(value) == 0 || len(value) > maximumWiFiBoardBytes {
					return errors.New("duplicate, empty or oversized ath12k board payload")
				}
				payload = value
			}
			return nil
		}); err != nil {
			return err
		}
		for name := range matches {
			if payload == nil {
				return errors.New("SP11 board selector has no calibration payload")
			}
			if _, exists := selections[name]; exists {
				return errors.New("ambiguous SP11 board selector across database entries")
			}
			selections[name] = payload
		}
		return nil
	})
	if err != nil {
		return wifiBoardSelection{}, err
	}
	for _, name := range []string{wifiSurfaceBoard, wifiFallbackBoard} {
		if payload, ok := selections[name]; ok {
			return wifiBoardSelection{name: name, data: bytes.Clone(payload)}, nil
		}
	}
	return wifiBoardSelection{}, errors.New("distribution board database contains neither the native SP11 entry nor its qualified fallback; update linux-firmware")
}

// wifiLimitedOutput fails once decompression crosses its retained-output limit.
type wifiLimitedOutput struct {
	bytes.Buffer
}

// Write refuses oversized decompressed output rather than silently truncating it.
func (output *wifiLimitedOutput) Write(data []byte) (int, error) {
	if len(data) > maximumWiFiDatabaseBytes-output.Len() {
		return 0, errors.New("expanded Wi-Fi board database exceeds its size limit")
	}
	return output.Buffer.Write(data)
}

// readWiFiDatabase snapshots a bounded regular distribution input. Decompression
// invokes only the host's fixed zstd binary with stdin, never a downloaded helper.
func (installer *Installer) readWiFiDatabase(ctx context.Context, root string) ([]byte, string, error) {
	for _, suffix := range []string{"", ".zst"} {
		path, err := resolveTarget(root, wifiFirmwareDirectory+"board-2.bin"+suffix)
		if err != nil {
			return nil, "", err
		}
		file, info, err := openRegularNoFollow(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		if info.Size() < 1 || info.Size() > maximumWiFiDatabaseBytes {
			_ = file.Close()
			return nil, "", errors.New("distribution Wi-Fi database is empty or oversized")
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maximumWiFiDatabaseBytes+1))
		endInfo, statErr := file.Stat()
		closeErr := file.Close()
		if err := errors.Join(readErr, statErr, closeErr); err != nil {
			return nil, "", err
		}
		if int64(len(data)) != info.Size() || endInfo.Size() != info.Size() || !endInfo.ModTime().Equal(info.ModTime()) {
			return nil, "", errors.New("distribution Wi-Fi database changed while reading")
		}
		if suffix == "" {
			return data, path, nil
		}
		boundedContext, cancel := context.WithTimeout(ctx, 15*time.Second)
		var output wifiLimitedOutput
		var diagnostic boundedActivationOutput
		err = installer.runner.Run(boundedContext, platform.Command{
			Name: "/usr/bin/zstd", Args: []string{"--decompress", "--stdout", "--quiet", "--memory=32MB"},
			Stdin: bytes.NewReader(data), Stdout: &output, Stderr: &diagnostic,
		})
		cancel()
		if err != nil {
			return nil, "", fmt.Errorf("decode distribution Wi-Fi database (install zstd if unavailable): %w: %s", err, diagnostic.String())
		}
		return output.Bytes(), path, nil
	}
	return nil, "", errors.New("distribution WCN7850 board-2.bin or board-2.bin.zst is missing; install linux-firmware")
}
