# Developer guide

You do not need to learn every Lexr trust boundary before fixing a typo or a
small bug. Start with the part you intend to change, then follow the links to the
decisions and tests which protect it.

## Choose your starting point

| You want to… | Read |
| --- | --- |
| Set up the repository and run the checks | [Development and testing](development.md) |
| Understand how the command is divided into features | [Architecture](architecture.md) |
| Add or update an image or userspace entry | [Catalogue maintenance](catalogues.md) |
| Make an explanation easier to use | [Documentation](documentation.md) |
| Propose a lasting contract or trust-boundary change | [Architecture decisions](architecture.md#architecture-decisions) |

The root [`CONTRIBUTING.md`](https://github.com/ooaklee/lexr.sh/blob/main/CONTRIBUTING.md)
is the canonical contribution policy. Read it before opening a substantial pull
request. It covers issue-first changes, privacy, commit style, changelog policy,
licensing and third-party material.

## Keep the change proportional

Small fixes, tests and documentation improvements can usually go straight to a
pull request. Discuss a substantial feature, persisted-data change, privileged
workflow or architecture change first. A short conversation is cheaper than
building against the wrong boundary.

Lexr handles disks, boot assets and private device evidence. Dry runs, repeated
validation, exact confirmation, rollback and redacted output are part of the
behaviour—not optional polish. When you change a workflow, preserve the safety
property as well as the successful path.

If you are here to operate Lexr rather than change it, the [user guide](../user-guide/index.md)
or [operator manual](../operator-manual/index.md) will get you to the relevant
task more quickly.
