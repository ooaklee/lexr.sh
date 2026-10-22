package kernel

// MaximumBundleManifestBytes is the shared byte ceiling for local and release
// delivery contracts. It accommodates complete multi-device Stubble selection
// evidence while keeping manifest acquisition and composition resource-bounded.
const MaximumBundleManifestBytes int64 = 1 << 20
