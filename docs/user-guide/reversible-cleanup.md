# Run reversible clean-up

Lexr clean-up removes only recognised legacy Surface Pro 11 workarounds from an exact reviewed plan and leaves durable recovery evidence. The workflow is deliberately separate from image creation and userspace installation so neither can silently remove system state.

## Audience and context

Use this page after diagnosis shows a recognised workaround that you have decided to remove. Start without privilege, inspect the generated JSON plan, and grant elevated access only to apply or restore the reviewed transaction.

## Prerequisites and limits

- The scanner considers only a fixed allow-list of known legacy paths and requires expected content or link targets to match.
- Applying a plan requires `--yes` and changes the selected target root.
- Keep the printed receipt, backup directory, and any quarantine content until recovery is no longer needed.
- Current recovery accepts only Lexr's own recovery hierarchy. Predecessor transactions require the exact binary which created them.

## 1. Scan and review the plan

Clean-up is deliberately separate from image creation. Start with a read-only scan, then write the exact JSON plan you intend to apply:

```sh
lexr clean scan
lexr clean plan --output lexr-cleanup-plan.json
cat lexr-cleanup-plan.json
sudo lexr clean apply \
  --root / \
  --plan lexr-cleanup-plan.json \
  --yes
```

The scanner considers only a fixed set of known legacy paths. Required content markers must match before a regular file is considered recognised. A known service-enablement link must resolve to the exact retired unit. Other links, unusual files, changed content, and entries created after planning are left for manual review.

## 2. Apply the reviewed transaction

Applying a plan requires `--yes`. Before the first original path changes, the CLI writes and flushes a prepared recovery receipt below `/var/lib/lexr/backups`. Each reviewed entry is then atomically moved into a private same-filesystem quarantine, verified again, copied into its durable backup, and removed from quarantine. A completed receipt is published only after every entry succeeds. If the operation is interrupted, `receipt.pending.json` maps any original, quarantine, and backup locations.

## 3. Restore when needed

Restore a prepared or completed transaction with the receipt path printed by `clean apply` or included in an interruption error:

```sh
sudo lexr clean restore \
  /var/lib/lexr/backups/<transaction>/receipt.json \
  --root / \
  --yes
```

Restoration verifies regular-file digests or exact symbolic-link text and refuses to overwrite locally changed content. Recovery copies remain available after a successful restore.

Current `clean restore` accepts only receipts and quarantine names created in Lexr's recovery hierarchy. Before upgrading, restore any predecessor clean-up transaction with the exact binary which created it. If recovery must be deferred, retain that binary together with its receipt, backups, quarantine content and target; do not rename those paths into the Lexr hierarchy.

## What clean-up will not remove

The allow-list currently covers selected system-wide audio routing helpers, in-tree touchscreen configuration hooks, and G6 service enablement. It does not automatically remove arbitrary out-of-tree modules, rebuild contaminated historical initramfs images, delete per-user configuration, or remove unfamiliar UCM data. Those findings require explicit manual diagnosis. A future kernel change can also make a workaround relevant again, so removal remains an operator decision.

## Success and next steps

A completed receipt means every reviewed entry was processed; it does not make unfamiliar findings safe to remove manually. Retain the recovery copies until you have checked the resulting system, and use the printed receipt if restoration becomes necessary.

Return to [userspace support](userspace-support.md) to inspect the current state again, or to the [user-guide index](index.md) for another workflow.
