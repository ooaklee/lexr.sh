package kernel

// RequestedBootImageMode records the build policy selected by the caller. It
// deliberately does not describe the generated image: source policy can
// produce either effective delivery mode.
type RequestedBootImageMode string

const (
	// RequestedBootImageModeSource preserves the kernel source's packaging policy.
	RequestedBootImageModeSource RequestedBootImageMode = "source"
	// RequestedBootImageModeStubble requires an image with embedded DTBs.
	RequestedBootImageModeStubble RequestedBootImageMode = "stubble"
	// RequestedBootImageModeNoStubble requires an image without embedded DTBs.
	RequestedBootImageModeNoStubble RequestedBootImageMode = "nostubble"
)

// DTBDelivery records the effective, structurally verified device-tree
// delivery contract produced by a build. It is distinct from an installed
// boot binding: external-required states an obligation which package lifecycle
// handling must later satisfy.
type DTBDelivery string

const (
	// DTBDeliveryEmbedded means the packaged image contains its attributable DTBs.
	DTBDeliveryEmbedded DTBDelivery = "embedded"
	// DTBDeliveryExternalRequired means boot support must materialise and bind a DTB.
	DTBDeliveryExternalRequired DTBDelivery = "external-required"
)

// DeviceTreeSelectorKind identifies one declared Stubble selection route.
type DeviceTreeSelectorKind string

const (
	// DeviceTreeSelectorHWID identifies a Stubble hardware-ID selector.
	DeviceTreeSelectorHWID DeviceTreeSelectorKind = "hwid"
	// DeviceTreeSelectorCompatible identifies a device-tree compatible selector.
	DeviceTreeSelectorCompatible DeviceTreeSelectorKind = "compatible"
)

// DeviceTreeSelector records one deterministic, public selection input for a
// packaged device tree.
type DeviceTreeSelector struct {
	// Kind identifies the selector namespace.
	Kind DeviceTreeSelectorKind `json:"kind"`
	// Value is the exact selector value supplied by the selected build profile.
	Value string `json:"value"`
}
