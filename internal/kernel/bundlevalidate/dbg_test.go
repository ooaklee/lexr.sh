package bundlevalidate

import (
	"context"
	"testing"
)

func TestDebugHeaderDeb(t *testing.T) {
	v := &Validator{DisableExternalTools: true}
	integrity, err := v.inspectDebNative(context.Background(), "/Volumes/WD_BLACK/Workspace/repo/linux-surface-pro/linux_ms_dev_kit-sp11/build/lexr/kout6/linux-qcom-x1e-headers-7.3.0-jg-1sp11v6_7.3.0-jg-1sp11v6_all.deb")
	t.Logf("integrity=%+v err=%v", integrity, err)
}
