package archlinux

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// installerFirmware lists the public firmware already verified in the live root.
var installerFirmware = []string{
	"qcom/gen70500_gmu.bin", "qcom/gen70500_sqe.fw",
	"ath12k/WCN7850/hw2.0/board.bin", "ath12k/WCN7850/hw2.0/board-2.bin",
	"ath12k/WCN7850/hw2.0/amss.bin", "ath12k/WCN7850/hw2.0/m3.bin",
}

// installerProfiles binds each supported device to its coherent kernel DTB.
var installerProfiles = map[string]string{
	"surface-pro-11-x1e-oled": "x1e80100-microsoft-denali-oled.dtb",
	"surface-pro-11-x1p-lcd":  "x1p64100-microsoft-denali.dtb",
}

// installerAssetPaths is the closed installation payload inventory, relative to sp11.
func installerAssetPaths() []string {
	paths := []string{"lexr-kernel-sp11.pkg.tar.gz", "installer/installed.conf", "installer/lexr_sp11", "installer/runtime.json"}
	for _, firmware := range installerFirmware {
		paths = append(paths, "installer/firmware/"+firmware)
	}
	for _, profile := range []string{"surface-pro-11-x1e-oled", "surface-pro-11-x1p-lcd"} {
		paths = append(paths, "installer/"+profile+".cfg")
	}
	return paths
}

// stageInstallerScripts copies only maintained compiler inputs into the workspace.
func stageInstallerScripts(workspace, abi string) error {
	directory := filepath.Join(workspace, "sp11", "installer")
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	for name, text := range map[string]string{
		"guided.py": installerGuided, "target.py": installerTarget,
		"installed.conf": InstalledInitramfsConfig(), "lexr_sp11": EarlySupportHook(),
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(text), 0644); err != nil {
			return err
		}
	}
	for profile := range installerProfiles {
		text, err := (InstalledBoot{ABI: abi, RootUUID: "00000000-0000-0000-0000-000000000000", Device: profile}).GRUBConfig()
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(directory, profile+".cfg"), []byte(text), 0644); err != nil {
			return err
		}
	}
	return nil
}

// installerExportScript copies firmware and runtime identities from the verified root.
const installerExportScript = `set -euo pipefail
python3 - "$1" <<'PY'
import hashlib,json,pathlib,shutil,sys
root=pathlib.Path('/linux-work/rootfs');out=pathlib.Path('/work/sp11/installer');abi=sys.argv[1]
firmware=('qcom/gen70500_gmu.bin','qcom/gen70500_sqe.fw','ath12k/WCN7850/hw2.0/board.bin','ath12k/WCN7850/hw2.0/board-2.bin','ath12k/WCN7850/hw2.0/amss.bin','ath12k/WCN7850/hw2.0/m3.bin')
for name in firmware:
 src=root/'usr/lib/firmware'/name;dst=out/'firmware'/name
 assert src.is_file() and src.resolve()==src
 dst.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(src,dst)
names=['boot/vmlinuz-'+abi]+['usr/lib/firmware/'+abi+'/device-tree/qcom/'+name for name in ('x1e80100-microsoft-denali-oled.dtb','x1p64100-microsoft-denali.dtb')]
records={}
for name in names:
 path=root/name;assert path.is_file() and path.resolve()==path
 with path.open('rb') as f: digest=hashlib.file_digest(f,'sha256').hexdigest()
 records[name]={'sha256':digest,'size':path.stat().st_size}
(out/'runtime.json').write_text(json.dumps(records,sort_keys=True,indent=2)+'\n')
PY
`

// writeInstallerManifest binds every install input before it is retained in the root.
func writeInstallerManifest(workspace, abi string) error {
	files := map[string]map[string]any{}
	for _, path := range installerAssetPaths() {
		record, err := recordFile(filepath.Join(workspace, "sp11", path), "sp11/"+path)
		if err != nil {
			return fmt.Errorf("installer payload: %w", err)
		}
		files[path] = map[string]any{"sha256": record.SHA256, "size": record.Size}
	}
	encoded, err := json.MarshalIndent(map[string]any{"schema": 1, "abi": abi, "profiles": installerProfiles, "files": files}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workspace, "sp11/installer/payload.json"), append(encoded, '\n'), 0644)
}
