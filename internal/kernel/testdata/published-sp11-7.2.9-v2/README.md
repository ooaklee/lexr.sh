# Published 7.2.9 v2 delivery metadata

`lexr-kernel-bundle.json` is the unmodified public manifest from the
[7.2.9 v2 OE release](https://github.com/ooaklee/linux-surface-pro-11-oe/releases/tag/sp11-qcom-x1e-7.2.9-jg-0sp11v2).
Its exact 178,937 bytes and SHA-256 are recorded in `SOURCES.json` and pinned by
the tests. This schema-3 document contains 39 ordered embedded-DTB declarations
and 501 HWID-table records, and exceeds the old 64 KiB manifest ceiling.

The offline tests exercise the ordinary manifest readers, canonical delivery
validation and integrity refusals. They never download a package, execute a
payload, install a kernel or contact hardware. In particular, an optional DTB
being present is not a claim that its device is supported or tested. The public
HWID records describe the release's selection inventory, not a private capture
of one person's machine.

`COPYING` and `LICENSE-GPL-2.0` are the unmodified notices distributed beside
the associated kernel release. Their origins and exact bytes are retained in
`SOURCES.json`; no kernel binary or source is redistributed by this fixture.
The [corresponding kernel source revision](https://github.com/ooaklee/linux_ms_dev_kit-sp11/tree/67f9630ab44a5f15dd565f1f1e4aaaf2a5c093cc)
retains its own file-specific terms. Including these release notices does not
relicense Lexr or change the terms of existing source files.
