package archlinux

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// validateInstaller reads payload data without executing code from the ISO.
func (v *Validator) validateInstaller(ctx context.Context, image, workspace, volume, abi string) error {
	expected := map[string]string{"installed.conf": InstalledInitramfsConfig(), "lexr_sp11": EarlySupportHook(), "guided.py": installerGuided, "policy.py": installerPolicy, "target.py": installerTarget}
	for profile := range installerProfiles {
		config, err := (InstalledBoot{ABI: abi, RootUUID: "00000000-0000-0000-0000-000000000000", Device: profile}).GRUBConfig()
		if err != nil {
			return err
		}
		expected[profile+".cfg"] = config
	}
	for name, text := range expected {
		actual, err := os.ReadFile(filepath.Join(workspace, "sp11/installer", name))
		if err != nil || string(actual) != text {
			return fmt.Errorf("installer asset differs: %s", name)
		}
	}
	paths, err := json.Marshal(installerAssetPaths())
	if err != nil {
		return err
	}
	return v.Docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "python3", "-c", inspectInstallerScript, abi, string(paths))
}

// inspectInstallerScript verifies retained copies, manifests and live firmware equality.
const inspectInstallerScript = `import hashlib,json,pathlib,sys
root=pathlib.Path('/linux-work/rootfs');media=pathlib.Path('/work/sp11');abi=sys.argv[1];paths=json.loads(sys.argv[2])
retained=root/'usr/share/lexr/arch-media/sp11'
manifest=json.loads((media/'installer/payload.json').read_text())
assert set(manifest)=={'schema','abi','profiles','files'} and manifest['schema']==1 and manifest['abi']==abi
profiles={'surface-pro-11-x1e-oled':'x1e80100-microsoft-denali-oled.dtb','surface-pro-11-x1p-lcd':'x1p64100-microsoft-denali.dtb'}
assert manifest['profiles']==profiles and set(manifest['files'])==set(paths)
def check(path):
 assert path.is_file() and path.resolve()==path,path
def identity(path):
 check(path)
 with path.open('rb') as f: sha=hashlib.file_digest(f,'sha256').hexdigest()
 return {'size':path.stat().st_size,'sha256':sha}
for name in paths:
 path=media/name
 assert identity(path)==manifest['files'][name],name
 assert identity(retained/name)==identity(path),name
 if name.startswith('installer/firmware/'):
  assert identity(root/'usr/lib/firmware'/name.removeprefix('installer/firmware/'))==identity(path),name
for name in ('payload.json','guided.py','policy.py','target.py'):
 assert identity(media/'installer'/name)==identity(retained/'installer'/name),name
runtime=json.loads((media/'installer/runtime.json').read_text())
expected={'boot/vmlinuz-'+abi}|{'usr/lib/firmware/'+abi+'/device-tree/qcom/'+tree for tree in profiles.values()}
assert set(runtime)==expected
for name,record in runtime.items(): assert identity(root/name)==record,name
`
