# Lexr documentation

Lexr brings the moving parts of experimental ARM64 Linux on Qualcomm Snapdragon
X devices into one checked workflow. Its current implementation and physical
test evidence centre on the Microsoft Surface Pro 11, Ooaklee's daily
driver. These guides keep the first steps short while leaving the privacy,
recovery, device scope and release details close to the actions they protect.

> [!WARNING]
> Lexr-generated media and its custom kernel are experimental. Back up important
> data, keep another bootable recovery device available, and disable Secure Boot
> before booting an unsigned custom kernel.

## Start here

Start with [Install Lexr](getting-started/install.md), then follow the
[first-image quickstart](getting-started/index.md). Choose your hardware once
with `lexr init` and reuse it for images, diagnostics and installation.

```sh
lexr profile list
lexr init x1e80100-microsoft-denali-oled
lexr config check
```

The example selects the Surface Pro 11 X Elite OLED. Choose the LCD profile
from the list for an X Plus device. A profile identifies your target; it does
not mean every workflow has been tested on that hardware.

## Choose the documentation for your job

| You are… | Documentation | What it covers |
| --- | --- | --- |
| Setting up or using Lexr | [User guide](user-guide/index.md) | Images, USB media, offline use, hardware support, Windows hand-offs and clean-up |
| Understanding saved defaults | [Hardware profiles](concepts/hardware-profiles.md) and [configuration](concepts/configuration-binding.md) | Choose a target, override it for one command, and reuse settings |
| Running trusted build or release operations | [Operator manual](operator-manual/index.md) | Kernel handling, release preparation, automation and publication boundaries |
| Changing the project | [Developer guide](developer-guide/index.md) | Architecture, local development, testing, catalogues and documentation |
| Looking up exact syntax or constraints | [Reference](reference/index.md) | Commands, configuration keys, requirements and project boundaries |

[Architecture decisions](developer-guide/architecture.md#architecture-decisions)
record why lasting trust and compatibility boundaries exist; task guides remain
the best place to learn what to do now.

## Documentation as source

The Markdown in this repository is the canonical documentation. It is organised
so the same files can be rendered by MkDocs without turning a generated
site or repository wiki into a second source of truth.

If you find a missing step or an explanation that assumes too much, a small
documentation pull request is welcome. See the
[documentation contribution guide](developer-guide/documentation.md).
