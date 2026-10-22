package identity

import "testing"

// TestParseDomains covers both maintained spellings and the closed registry.
func TestParseDomains(t *testing.T) {
	for _, abi := range []string{"7.2.0-rc5-sp11v19-qcom-x1e", "7.2.0-jg-0sp11v19-qcom-x1e", "7.2.2-jg-0sl7v1-qcom-x1e", "7.2.2-jg-0x1ev1-qcom-x1e", "7.2.2-jg-0x1pv1-qcom-x1e", "7.2.2-jg-0x1v1-qcom-x1e"} {
		if _, err := Parse(abi); err != nil {
			t.Errorf("%s: %v", abi, err)
		}
	}
	for _, abi := range []string{"7.2.0-sp11v01-qcom-x1e", "7.2.0-sp11v0-qcom-x1e", "07.2.0-sp11v1-qcom-x1e", "7.2.0-sp11v1-sl7v2-qcom-x1e", "7.2.2-jg-0abcdev1-qcom-x1e", "7.2.2-jg-0sp11v1.0~beta1-qcom-x1e"} {
		if _, err := Parse(abi); err == nil {
			t.Errorf("accepted %s", abi)
		}
	}
}

// TestGenerationsHaveNoCrossDomainOrder proves unrelated scopes never sort.
func TestGenerationsHaveNoCrossDomainOrder(t *testing.T) {
	a, _ := Parse("7.2.0-jg-0sp11v19-qcom-x1e")
	for _, abi := range []string{"7.2.2-jg-0sp11v1-qcom-x1e", "7.2.0-jg-0sl7v20-qcom-x1e", "7.2.0-rc5-jg-0sp11v19-qcom-x1e"} {
		b, _ := Parse(abi)
		if _, err := CompareGeneration(a, b); err == nil {
			t.Errorf("cross-domain order for %s", abi)
		}
	}
	b, _ := Parse("7.2.0-jg-0sp11v100-qcom-x1e")
	if n, err := CompareGeneration(a, b); err != nil || n != -1 {
		t.Fatalf("numeric order=%d,%v", n, err)
	}
}

// TestPackageIterationDoesNotEnterABI keeps package-only changes out of identity.
func TestPackageIterationDoesNotEnterABI(t *testing.T) {
	for _, version := range []string{"7.2.2-jg-0sp11v1.0~beta1", "7.2.2-jg-0sp11v1.0", "7.2.2-jg-0sp11v1.1"} {
		abi, _, err := ABIFromPackage(version, "qcom-x1e")
		if err != nil || abi != "7.2.2-jg-0sp11v1-qcom-x1e" {
			t.Fatalf("%s: %s %v", version, abi, err)
		}
	}
}
