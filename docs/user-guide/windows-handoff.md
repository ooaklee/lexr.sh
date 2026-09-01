# Complete the private Windows hand-off

Use the Windows hand-off to collect the authorised platform firmware and Bluetooth public controller address that must come from Windows on the same physical Surface Pro 11, then import and apply that bounded evidence on Linux. The workflow keeps those values private, device-bound, and separate from images and userspace releases.

> A collected hand-off is private device data. Do not add its directory, manifest, or payloads to an image, release, issue, diagnostic archive, or source checkout.

## Audience and context

This workflow is for an operator who can use an elevated Windows PowerShell 5.1 session on the target Surface Pro 11, transfer a private directory under physical control, and then manage it as an unprivileged Linux user. Applying or restoring the hand-off is a separate privileged transaction.

Some Surface Pro 11 platform firmware and the Bluetooth public controller address must come from an authorised Windows installation on the same device. They are private device data, not a userspace release and not an ISO companion. Do not add a collected hand-off directory, its manifest, or any of its payloads to an image, release, issue, diagnostic archive, or source checkout.

The canonical Windows collector is `tools/collect-sp11-windows-handoff.ps1` in the CLI source tree and emits one strict directory. It is also present in the companion source archive, so a user can extract that ordinary non-private script from the live medium before running it in Windows. Contract version 3 and collector `3.0.0` use a fresh random salt and a domain-separated SMBIOS UUID binding for same-device application. The raw SMBIOS UUID is never exported. A selected Bluetooth adapter instance identifier remains private in-memory collection evidence and is not exported as either raw text or a digest. Platform firmware is an all-or-absent eleven-file set with fixed destinations, copied-byte digests, and Windows DriverStore provenance; every file must come from its exact compiled original INF basename rather than a mutable `oemN.inf` alias or a filename-only match. Windows Wi-Fi firmware is deliberately excluded because Linux board firmware remains owned by the distribution firmware package.

Contract versions 1 and 2 were unpublished pre-release shapes. The current CLI does not import, list, purge or apply them, and it never silently treats their state as Lexr state. Before upgrading, use the exact predecessor binary which created any stored pre-release entry to complete its reviewed purge, then recollect with collector `3.0.0`. A transferred version 1 or version 2 source directory is not valid version 3 input and must not be reused.

## 1. Create the protected Windows parent

Run collection from an elevated Windows PowerShell 5.1 session. First create one new protected parent on a local fixed NTFS volume. The following locale-independent commands set the exact owner and access rules required by the collector:

```powershell
$privateParent = Join-Path $env:ProgramFiles 'lexr-private'
if ([System.IO.Directory]::Exists($privateParent) -or
    [System.IO.File]::Exists($privateParent)) {
    throw 'Choose a new private parent; do not reset an existing directory.'
}
[void][System.IO.Directory]::CreateDirectory($privateParent)

$administrators = New-Object System.Security.Principal.SecurityIdentifier('S-1-5-32-544')
$localSystem = New-Object System.Security.Principal.SecurityIdentifier('S-1-5-18')
$inheritance = [System.Security.AccessControl.InheritanceFlags]::ContainerInherit -bor `
    [System.Security.AccessControl.InheritanceFlags]::ObjectInherit
$propagation = [System.Security.AccessControl.PropagationFlags]::None
$allow = [System.Security.AccessControl.AccessControlType]::Allow
$fullControl = [System.Security.AccessControl.FileSystemRights]::FullControl

$security = New-Object System.Security.AccessControl.DirectorySecurity
$security.SetOwner($administrators)
$security.SetAccessRuleProtection($true, $false)
[void]$security.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule(
    $administrators, $fullControl, $inheritance, $propagation, $allow)))
[void]$security.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule(
    $localSystem, $fullControl, $inheritance, $propagation, $allow)))
[System.IO.Directory]::SetAccessControl($privateParent, $security)
```

Use the stock `Program Files` directory as the protected parent's immediate ancestor. Its default ACL grants unprivileged principals read and execute access only, while the stock filesystem-root ACL grants create-directory access on the root and makes its broader Modify entry inherit-only. Do not put the parent beneath `ProgramData`: its stock Users rule grants write-attribute and write-extended-attribute access to that ancestor, which the collector deliberately rejects at this privileged boundary even when the new child has the exact private ACL.

## 2. Collect into a new child

Choose a new child name for every collection; the requested child must not already exist. Unplug external Bluetooth radios before collecting Bluetooth evidence, then run the collector from the CLI source tree:

```powershell
$handoff = Join-Path $privateParent `
    ('sp11-handoff-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))

& powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
    .\tools\collect-sp11-windows-handoff.ps1 `
    -OutputDirectory $handoff
if ($LASTEXITCODE -ne 0) {
    throw 'Windows hand-off collection failed.'
}
```

The default Bluetooth source is the sole network-adapter `PermanentAddress` whose structured PnP ancestry reaches the exact built-in WCN7850 radio. Add `-UseBTHPORTRegistry` only when you also want the collector to require the sole valid local BTHPORT address to agree exactly with that independently correlated value. The built-in radio and transport identities are `QCA_SHB\UART_H4_HMT` and `ACPI\QCOM0D04`; attached or ambiguous physical radios fail closed.

The parent check walks from the filesystem root without following reparse points, requires trusted ownership, rejects access which could redirect the privileged path, and retains filesystem object identities across writes and publication. Staging receives the same private DACL. Publication is a no-replace same-parent move, and failure cleanup enumerates and removes only checked entries without recursive traversal through a reparse object.

## 3. Transfer only the completed child

Do not collect directly onto FAT, exFAT, a network share, or an unprotected directory. The implementation can accept a local removable NTFS parent only when the identical ACL, ancestor, and no-reparse policy passes, but collecting on fixed local NTFS and transferring only the completed child gives the clearest boundary. Copy that complete child to a new directory on trusted removable storage after the collector reports success:

```powershell
$transferRoot = 'E:\lexr-private-transfer'
if ([System.IO.Directory]::Exists($transferRoot) -or
    [System.IO.File]::Exists($transferRoot)) {
    throw 'Choose a new empty transfer directory.'
}
[void][System.IO.Directory]::CreateDirectory($transferRoot)
Copy-Item -LiteralPath $handoff -Destination $transferRoot -Recurse -ErrorAction Stop
```

The removable copy is private even when its filesystem cannot preserve Windows ACLs. It is a transfer copy, never the live privileged output transaction. Keep it physically controlled and remove unneeded copies after Linux import has been verified.

## 4. Import into the private Linux store

Copy the completed hand-off directory from the private medium to the Linux system, then import it as the same unprivileged user who will manage it:

```sh
HANDOFF_STORE="${HOME}/.lexr-handoffs"
lexr handoff import <windows-handoff-directory> --store "$HANDOFF_STORE"
lexr handoff list --store "$HANDOFF_STORE"
```

`$HOME` is expanded by the unprivileged shell, so `HANDOFF_STORE` is the absolute path to that user's private store. Keep this same value for every import, inspection, application, and retention command; in particular, the shell expands it before `sudo`, preventing privileged application from selecting root's separate default store.

Import rejects unknown or mis-cased JSON fields, missing or extra files, symbolic links, special files, case-colliding paths, non-canonical mappings, digest or size mismatches, and source mutation during verification. It publishes the exact bytes atomically beneath a mode-`0700` content-addressed store and protects every stored file with mode `0600`. Re-importing identical bytes revalidates and reuses the existing entry. Ordinary and JSON output contain only redacted summary fields; they never contain the Bluetooth address, raw UUID, adapter identifier, salts, or their bindings.

## 5. Review and apply on Linux

Applying an imported hand-off is a separate, privileged transaction. The command revalidates the stored closed set, proves that the live SMBIOS identity at `--identity-root` matches the device-bound evidence, and prepares changes only beneath the mandatory `--target-root`. The default identity root is `/`; keep it when preparing another mounted root on the same Surface. Use `--feature firmware` or `--feature bluetooth` to select one included feature, or omit the repeatable flag to select every included feature. Firmware application also requires an explicit aDSP policy: `enabled` for an installed system whose root is on internal storage, or `disabled` for a live USB root.

Review the immutable plan as an unprivileged user before applying it. The dry run prints the exact ID-, policy-, target-, and current-state-bound confirmation phrase:

```sh
lexr handoff apply <id> \
  --store "$HANDOFF_STORE" \
  --target-root /target \
  --feature firmware \
  --feature bluetooth \
  --adsp-policy enabled \
  --dry-run

sudo lexr handoff apply <id> \
  --store "$HANDOFF_STORE" \
  --target-root /target \
  --feature firmware \
  --feature bluetooth \
  --adsp-policy enabled \
  --confirm '<exact phrase from the current dry run>'
```

For the running live system, spell the target explicitly as `--target-root /` and select `--adsp-policy disabled` when firmware is included. The transaction installs only the fixed eleven-file platform-firmware set, its Denali GPU link, the selected aDSP policy, and the private Bluetooth runtime integration represented by the imported evidence. It does not copy Windows Wi-Fi firmware, change an unselected feature, expose private values in output, or accept a confirmation generated for another plan.

Bluetooth application records the compiled selector `surface-pro-11-wcn7850-uart`, never a boot-order-dependent numeric index. At service start, the Linux helper scans `/sys/class/bluetooth/hciN/device/of_node/compatible` for the exact NUL-delimited `qcom,wcn7850-bt` token supplied by the Surface Pro 11 UART device-tree node. An external controller cannot acquire authority by appearing as `hci0`; no built-in match times out without issuing an HCI address mutation, and multiple matching candidates fail as ambiguous.

## 6. Restore a started application

Every started mutation keeps private same-filesystem backups and a durable receipt beneath the target. A failure attempts bounded rollback but deliberately retains the receipt and backups for inspection or recovery. Restore is therefore explicit and uses its own current-state-bound confirmation:

Run the restore preview with elevation too: the privileged application creates its receipt directory as private root-owned state, so an unprivileged preview normally cannot read it.

```sh
sudo lexr handoff restore <receipt-id> \
  --target-root /target \
  --dry-run

sudo lexr handoff restore <receipt-id> \
  --target-root /target \
  --confirm '<exact phrase from the current restore dry run>'
```

Do not delete a retained receipt or its private backup directory until application, boot validation, and any necessary restoration have completed. `--json` is available for redacted automation output on both commands.

The current Lexr restore path accepts only schema-2 application receipts. Before upgrading, use the exact predecessor binary which created a schema-1 receipt to finish restoration. If restoration must be deferred, retain that exact binary together with the receipt, backups and target until recovery is complete; a schema-1 receipt cannot be recreated safely from current state.

## 7. Review retention and purge stored input

Review retention before deleting an entry from the same explicit private store:

```sh
lexr handoff purge <id> --store "$HANDOFF_STORE" --dry-run
lexr handoff purge <id> --store "$HANDOFF_STORE" --confirm 'purge <id>'
```

Purge accepts only the complete content-addressed phrase, revalidates the private closed set, atomically isolates that exact direct child, revalidates it again, and removes only the verified files and directories. The current command accepts only Lexr's version 3 store entries. Use the exact predecessor binary which created a version 1 or version 2 entry to purge that state before upgrading; recursive manual deletion is not a supported substitute. Purging a current stored entry does not remove application receipts or backups from a target root; recover or deliberately retain those records independently.

## Success and next steps

Host-independent tests do not replace maintained-hardware qualification. Successful collection on supported Windows, private transfer, same-device Linux import and application, Bluetooth address programming, firmware loading, cold boot, and restoration on the same physical Surface Pro 11 remain release gates. [ADR022](../adr/adr-022-privileged-windows-collection-and-controller-authority.md) records the privileged storage and controller-authority decision together with the reviewed Microsoft driver-package evidence.

After checking the hand-off on the device, [inspect userspace support](userspace-support.md). Keep application receipts and backups until boot validation and any necessary restoration are complete, even if you purge the imported store entry.
