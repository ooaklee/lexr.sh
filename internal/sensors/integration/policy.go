package integration

import "github.com/ooaklee/lexr.sh/internal/sensors/packageplan"

// Fixed names describe a manual, qualification-only service. BusName is an
// external readiness identity, deliberately not a systemd BusName= directive.
const (
	UnitName       = "lexr-native-sensors.service"
	VendorUnitName = "iio-sensor-proxy.service"
	BusName        = "net.hadess.SensorProxy"
	LoaderPath     = "/lib/ld-linux-aarch64.so.1"
	Kind           = "lexr.sensor-integration-qualification-only"
	unitAsset      = "rendered/" + UnitName
	programPath    = packageplan.PrivatePrefix + "/libexec/iio-sensor-proxy"
	libraryPath    = packageplan.PrivatePrefix + "/lib"
)

// templatePolicy fixes exact authenticated upstream bytes and their passive
// bundle names. None of these names is a registration destination.
type templatePolicy struct {
	sourcePath, assetPath, sha256 string
	bytes                         int64
}

// templatePolicies returns the four independently reviewed integration inputs.
// The complete Captured source composition is independently verified as well.
func templatePolicies() []templatePolicy {
	return []templatePolicy{
		{"iio-sensor-proxy/source/data/80-iio-sensor-proxy.rules", "raw/80-iio-sensor-proxy.rules", "3d50c919f17e5140921fd6629f7d51ed89daef3a345e6ebaf70610c6299397d6", 2167},
		{"iio-sensor-proxy/source/data/iio-sensor-proxy.service.in", "raw/iio-sensor-proxy.service.in", "fa01303cc8f725ab3b666d223dbb0ffed32aad5e96391942c2e0ea3c15cb3422", 435},
		{"iio-sensor-proxy/source/data/net.hadess.SensorProxy.conf.in", "raw/net.hadess.SensorProxy.conf.in", "33c9d5ee47c839a640ce549c57ef91b85ccab7dd4a8097c3225662655030fb25", 1261},
		{"iio-sensor-proxy/source/data/net.hadess.SensorProxy.policy", "raw/net.hadess.SensorProxy.policy", "035194978c2d9f3ca53af81e1936334ffd97f6efeabdf44420f52e74dc8fcea9", 686},
	}
}

// fixedArgv contains no shell, environment override, replacement option or
// caller-selected executable, provider directory or argument.
func fixedArgv() []string {
	return []string{LoaderPath, "--library-path", libraryPath, programPath}
}

// renderedUnit is an inert, static manual unit. Type=exec intentionally avoids
// a second systemd watcher for the vendor's shared D-Bus name. Separate external
// owner verification is required after any future explicitly approved start.
// There are no installation, enablement, conflicts or hardware-trigger hooks.
const renderedUnit = `[Unit]
Description=Lexr native sensor proxy (qualification only)
Requires=dbus.socket
After=dbus.socket

[Service]
Type=exec
ExecStart=` + LoaderPath + ` --library-path ` + libraryPath + ` ` + programPath + `
Restart=no

# Retained source hardening; this unit has not qualified physical access.
ProtectSystem=strict
ProtectControlGroups=true
ProtectHome=true
ProtectKernelModules=true
PrivateTmp=true
RestrictAddressFamilies=AF_UNIX AF_LOCAL AF_NETLINK AF_QIPCRTR
MemoryDenyWriteExecute=true
RestrictRealtime=true
`
