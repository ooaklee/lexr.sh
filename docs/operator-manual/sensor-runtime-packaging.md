# Qualify a sensor runtime package

Use this procedure to review the boundary between verified native sensor build
outputs and a future installable userspace component. The internal qualification
package is not a public sensor release or sensor installation command. Keep the
experimental source builder and existing userspace installation paths separate.

## Version-scoped successor qualification

The dated 2026-10-22 package and loader records below describe the exact 0.4.7
composition. The downstream 0.4.8 development successor uses separate fixed
qualification APIs and independently pinned source; neither package inherits
the other's evidence. Default entry points remain on 0.4.7, and mixed source,
artefact or package handles must be rejected.

The successor requires all 107 exact public SSC functions, including the 97
older name/version assignments. Its complete producer archive has eighteen
regular members and three aliases, adding two metadata-test logs to the old
sixteen-member closure. Both real metadata executables and their six helper
and five parser cases must pass without skips. The runtime layout remains nine
private files and three aliases; the new source archive has 968 members.

Successor package metadata describes static archive verification and compiled
recipe requirements, not observed execution. Record actual producer source,
image reuse, script, invocation, terminal state and output identities separately.
Then run a new package-pinned ARM64 relocated-loader proof and fresh-root
install/repeat/conflict/tamper/rollback checks. An accepted library build alone
does not close those gates. The compatibility fixture remains unverified, and
no public installer, service registration or hardware support is enabled.

[ADR054](../adr/adr-054-version-scoped-sensor-qualification.md) records the
additive boundary. Pending successor checks must not be described as passed
by borrowing the historical results below.

### Explicit successor service binding

The locally qualified 0.4.8 package has a separate inactive service path:
`integration.Render048` and `lifecycle.NewQualification048`. Both require the
matching opaque profile and authenticated package/source pair. The original
constructors remain fixed to 0.4.7; identical private paths and static unit bytes
do not permit mixing runtime identities.

The successor renders the same six passive assets without changing the
accepted runtime package or writing registration paths. Its lifecycle tests
use fresh roots and simulated service-manager/process/bus observations. They
must not be described as real systemd handover or activation tests.

[ADR055](../adr/adr-055-version-scoped-sensor-service-qualification.md) records
this limited advancement and the next real-manager gate. A disposable ARM64
Linux environment with its own system bus and cgroups is required before
qualifying a live backend. The public installer, physical-sensor tests and
compatibility support declarations remain separate work.

### Explicit successor source production

The hidden sensor source builder has an opt-in
`--sensor-source-profile metadata-048` selection. The default remains
`legacy-047`; the option is not valid for other userspace components.
Each selection uses its own compiled source, snapshot, recipe, ownership,
decoder and publication contract. A result cannot select its own policy.

This produces development artifacts only. Software and mocked-controller
tests alone are not a fresh native producer run, and the saved direct-recipe
0.4.8 package must not be relabelled as a normal CLI build. Keep actual producer
and downstream qualification receipts separate. No installer or service
activation is enabled by this option.

[ADR056](../adr/adr-056-explicit-sensor-producer-profiles.md) records this
additive source-production boundary.

#### Current normal-CLI evidence — 2026-10-23

The reviewed local changes based on `e76d19f` completed a fresh normal CLI
`metadata-048` producer and durable local development-output publication. All
three build-phase containers exited zero. Seven proxy fixture programs and
two metadata programs passed; the proxy recorded 304 executed cases and one
explicit French-locale skip, while all eleven metadata cases ran. This is not
a claim that all 316 listed cases executed, a public asset upload or physical
sensor qualification.

Strict saved-output verification and host package composition then produced
the 51,671,040-byte qualification package, SHA-256
`289fa8f2977cd398e8e9547fbcda98260193eb6dc333fc895c02ff4da27842ae`.
Passive host install/repeat/rollback and conflict/tamper refusal checks passed.
The exact package and authenticated source also passed six-asset inactive
rendering, version-pairing and simulated service apply/repeat/rollback with
both initially active and inactive vendor fixtures. A legacy process identity
substitution was refused. No proxy or real service manager was activated by
those host checks.

The earlier direct-recipe 0.4.8 package has identical bytes and separately
recorded ARM64 loader/native-offline evidence. Preserve that evidence as its
own observation; it was not rerun by the normal CLI package follow-through.
The later qualification receipts still leave these gates open:

- Fresh package-pinned ARM64 relocated-loader checks, native Linux offline
  transactions and service-contract replay for the normal-CLI follow-through.
- Controlled real systemd/D-Bus handover and verified rollback.
- Physical sensor roles, axes, calibration, clocks, desktop behaviour and
  suspend/resume qualification.
- Public installer, production support declaration and binary-release acceptance.

The earlier stopped live rehearsal and verified stock-configuration recovery
do not close the real-manager gate. The shipped catalogue, 0.4.7 defaults and
disabled sensor installation/activation boundaries remain unchanged. Reproduce
the source-only build below only as an explicitly selected qualification run;
successful source production does not grant any of those later actions.

#### Reproduce the source-build selection

This is a from-source qualification procedure for the reviewed change. Do not
assume an installed release contains the hidden selector. Record the exact
Lexr source revision and independently approved OE source composition.

The sensor component is deliberately absent from the shipped catalogue.
Save this bounded build-only override as `sensor-source-only.json`:

```json
{
  "schema_version": 2,
  "description": "Local sensor source-build qualification only.",
  "components": [{
    "id": "native-sensors",
    "name": "Native sensor development sources",
    "level": "experimental",
    "capability": "sensors",
    "redistribution": "source-required",
    "support_actions": {
      "status": false,
      "pull": false,
      "build": true,
      "install": false
    },
    "compatibility_evidence": "unqualified_source",
    "notes": ["Development artifacts only; no hardware qualification."],
    "remediation": "Leave distribution sensor services unchanged."
  }]
}
```

From the reviewed Lexr checkout, compile a distinct binary:

```sh
go run ./cmd/lexr-build/main.go --output bin/lexr-sensor-qualification
```

Set `SENSOR_CATALOG` to the override's absolute path and `OE_ROOT` to the exact
approved OE checkout. Set `SENSOR_WORK` and `SENSOR_OUTPUT` to existing,
canonical, current-user-owned private directories, separate from each other
and outside both source trees. Use mode `0700`; do not use `sudo`.

Request a read-only plan without contacting Docker or producing build outputs:

```sh
./bin/lexr-sensor-qualification \
  --userspace-catalog "$SENSOR_CATALOG" \
  userspace build sensors \
  --sensor-source-profile metadata-048 \
  --repository-root "$OE_ROOT" \
  --sensor-work-parent "$SENSOR_WORK" \
  --output-dir "$SENSOR_OUTPUT" \
  --jobs 2 \
  --dry-run --json
```

On a supported ARM64 Linux/macOS controller, inspect the authenticated source
composition, selected profile and recipe digests. An unsupported controller
returns an execution blocker, not source-authentication proof. A dry-run does
not establish Docker readiness or reserve capacity. Actual production also
requires an identified local native ARM64 Docker engine.

Only after separate approval, run the same command without `--dry-run`.
Retain outputs and uncertain resources on failure; do not automatically retry.
Do not add image, volume, hardware-profile or deployment-compatibility
overrides. The catalogue and selector grant source production only, not
installation, service activation or hardware qualification.

## Start with the right evidence

The accepted source composition combines pinned QMI, SSC and desktop proxy
sources. Its normal native producer completed compilation, software fixtures
and local development-output publication. That evidence is recorded in
[ADR045](../adr/adr-045-local-sensor-build-transport-and-bind-namespaces.md#2026-10-22-local-producer-evidence).
It does not qualify a relocated runtime, service activation or physical sensors.

The development archive contains 16 regular files and three library aliases.
It is not a package: systemd, udev, D-Bus and polkit integration bytes are not
included, and its build reports are not independent deployment authority.
Their six logical upstream destinations are observations, not missing pieces
to install globally; the proposed private layout deliberately relocates the
monitor and proxy. Do not copy it over distribution libraries or add it to an
installable catalogue entry.

## Review the inert layout

The internal package planner accepts only the strict decoder's verified
artefacts. It has no filesystem or execution operations. Its proposed runtime
identity is `native-sensors-runtime-v1`, separate from build-only
`native-sensors`, and its proposed release is `sp11-native-sensors-v1`.

The planned private prefix is
`/usr/lib/lexr/sensors/sp11-native-sensors-v1/`:

| Subdirectory | Planned contents |
| --- | --- |
| `lib/` | The exact QMI and SSC provider libraries and three relative aliases |
| `bin/` | `ssccli` and `monitor-sensor` |
| `libexec/` | `iio-sensor-proxy` |
| `share/licenses/` | The four retained licence files |

All three relative aliases, including the two-hop QMI route, remain exact.
The seven development/evidence files are not staged runtime payloads or
automatically publishable diagnostics. The plan records approved source
composition, byte identities, fixed paths and pending gates. Its four source
inventory/checksum identities and required recipe hashes are expected policy,
not proof of the saved archive's producer. It always reports
`qualification_required`, with installation and activation disabled. It does
not emit a package, invoke a target binary or create a new CLI workflow.

## Exercise the internal qualification contract

The qualification-only package has five canonical outer assets: a runtime
payload, corresponding source, the independently pinned qualification
declaration, package metadata and checksums. The runtime contains the nine
private files and three aliases above. The source bundle retains all approved
raw source, metadata, links, notices and the three current recipes. Provenance
explicitly records saved-archive replay and does not claim those recipes
produced the old binaries.

The separate OE packaging declaration is a software fixture, not a production
support declaration. Its bounded tuple is ARM64/SP11 OLED/Ubuntu 26.04/scoped
7.2.9 generation 2, with stable Lexr 0.7.0 minimum and no tested evidence.
Matching assessments remain `unverified` and require a dedicated experiment
override. Hard mismatches remain blocked. Existing component pins and the
build-only sensor catalogue ceiling are unchanged.

The offline qualifier can create only a fresh private root. It cannot adopt an
existing system or `/`. Planning writes nothing. Installation stages the closed
prefix plus passive declaration and receipt, synchronises it and publishes with
an exclusive descriptor-relative rename. Repeating the same exact installation
through its process-local handle is idempotent; changed or unknown contents
refuse without overwrite. Rollback validates ownership and contents again,
quarantines the exact tree and removes only known members and owned empty
directories. Journals live outside the target fixture and are not deletion
authority. Retain uncertain partials on failure.

The native software proof uses an isolated, pinned ARM64 dependency image and
read-only fresh payloads at the final private prefix. It checks every loader
row, exact private providers and aliases, safe help/version exits, and a
missing-provider inverse. Native Linux filesystem tests complement host tests.
Do not interpret these software checks as discovery, activation, physical
sensor qualification or support for another distribution.

[ADR051](../adr/adr-051-sensor-qualification-packages-and-offline-transactions.md)
records this additional internal workflow. It does not enable live deployment,
restart adoption, crash recovery or managed upgrades.

## Complete the remaining gates in order

1. **Declare an explicit experiment.** Review the architecture, device, exact
   distribution and version, scoped kernel domain and hard generation bounds.
   Do not borrow kernel qualification as sensor evidence. Author the canonical
   component declaration only after those bounds are justified, then review its
   independent Lexr pin. The `sensors` capability token alone grants no action.
2. **Close the package inputs.** Bind exact payload bytes, source composition,
   corresponding source, notices, build recipe and provenance. Address the
   applicable GPL/LGPL distribution and relinking requirements before public
   binary publication. Do not treat an adjacent checksum or build report as
   independent release authority.
3. **Prove the relocated loader.** Use fresh native software checks to show
   that the final private launcher selects the exact intended QMI and SSC
   libraries and compatible target dependencies. The producer's original
   no-RPATH ELF evidence proves only `/work/job/providers/lib`, not the runtime
   prefix. The separate dated qualification below covers the new route. Do not use
   global `ldconfig`, a global library search path or replacement distribution
   libraries as a shortcut.
4. **Qualify passive installation and recovery.** Test a fresh offline root,
   repeated installation, unmanaged conflicts, partial
   failures and exact restoration. Execute no target binaries. Keep all global
   service, udev, D-Bus and polkit locations unchanged. Managed upgrades and
   restart recovery need additional qualification; staging a udev rule can
   cause later automatic activation even without an explicit start command.
5. **Qualify exclusive activation.** The proxy owns the global
   `net.hadess.SensorProxy` identity and cannot run concurrently with the
   distribution proxy. Review a separate explicit transaction which records
   and restores prior units, drop-ins, masks, active state and ownership.
   Exercise failure and uninstall recovery before enabling any automatic rule.
6. **Qualify the physical target.** Obtain separate approval for discovery and
   streaming. Establish sensor identity, physical roles, axes/mounting,
   calibration, timestamps and clocks, then desktop behaviour and
   suspend/resume. Record each tested kernel/OS/device tuple without extending
   it to another profile or distribution.

The software and lifecycle gates precede any proposal to enable an opt-in
experimental installer. Physical tests separately establish tested target
evidence and support claims; an experimental installer may support that work
only after its earlier gates pass and the target experiment is explicitly
approved. Missing hardware evidence must not be marked tested merely to make
the experiment possible. This foundation enables no installation.

An absent kernel `tested_through_inclusive` records no kernel test evidence and
evaluates as `unverified` when hard bounds match. Explicit zero and null are not
valid persisted substitutes. The dedicated unverified override records an
experiment; it cannot override a hard incompatibility, missing identity or
disabled compiled action. See the
[component compatibility reference](../reference/component-compatibility.md).

## Local software qualification — 2026-10-22

The qualification package was emitted and reproduced byte-for-byte from the
retained development archive and authenticated corresponding source. It is
51,598,336 bytes, SHA-256
`ca05c65c6658c11f4fc9f2711290e063b186ac4c904fd0a6a7cd96718027eb08`.
Build/decode, defensive-copy and 14 rehashed corruption controls passed. This
was saved-archive replay, not a fresh sensor producer or public release.

One isolated native ARM64 run in the pinned dependency image passed all five
loader listings at the final private prefix, four help/version exits and the
missing-provider inverse. Twenty-two resolved library routes were hashed.
The strict independent Go verifier accepted the exact saved observation; no
unexpected rows were discarded or parser rules relaxed.

The actual package passed passive fresh install, exact repeat and content/mode
rollback on both the host and native Linux. The native fixture suite passed
74 tests/subtests, including path/conflict and retained-failure controls. The
full host race suite passed 5,195 tests/subtests across 66 packages, with vet and
all six CLI target builds passing. Strict documentation validation also passed.

An initial private controller stopped before execution because Docker ordered
its environment override before the inherited image variables. The exact
expectation was corrected with one positive and 12 refusal controls; the same
never-started helper was rechecked and then executed once. No native producer
was retried. Existing builds and volumes, the installed CLI and the Surface
were unchanged.

This qualifies only the scoped software fixture, with the host's normal umask
and the pinned native image. Other environments may fail closed and are not
qualified. Live-root installation, managed upgrades, restart/crash recovery,
service activation and physical sensor support remain open.

## Report only the stage that passed

### Service handover remains a separate qualification

The additional [ADR052](../adr/adr-052-isolated-sensor-service-lifecycle-qualification.md)
workflow retains a standalone inactive bundle: four exact source templates,
the proxy licence and a rendered manual unit. It does not add files to the
accepted runtime package, register a rule or policy, or start a proxy.

Its unique unit uses `Type=exec` without a `BusName` property, avoiding two
loaded systemd units watching the same name. Explicit ownership checks are
still required: successful process execution is not proof of the intended
D-Bus owner or of sensor readiness. Starting the proxy itself discovers
sensors and requires separate hardware-operation approval.

The executable lifecycle model uses only a closed fixture backend. It models
runtime exclusion before stopping the known vendor, observed bus release,
private-unit registration/start and independent ownership checks. On failure
it must prove our unit quiescent before removing exact owned files and
restoring the vendor's prior functional state. Unknown owners, substituted
files or an uncertain stop require retained recovery evidence, not a competing
start. A process-local handle is not restart/crash-recovery authority.

Fresh-root fixture results cannot establish actual systemd mask precedence,
D-Bus policy interaction, loader process attestation or firmware quiescence.
No production adapter or live installation action is provided. Qualification
of that separate adapter and an explicitly approved physical experiment remain
necessary before desktop or support claims.

### Local service-fixture evidence — 2026-10-22

The accepted package and authenticated source rendered all six inert assets
deterministically. Its exact generated unit has SHA-256
`2fc2d8ef2e555c65ee168be7d37f9919f0f0cfe22effbb1648849223e0936c2d`.
Actual-package apply/repeat/rollback passed with both initially active and
inactive vendor fixtures. File changes occurred only in fresh roots; service,
bus and process observations were simulated. No proxy was started.

Failure controls cover cancellation, errors before and after effects, delayed
ownership, uncertain stops, changed process epochs, byte-identical foreign
files, special-file substitutions and loaded-definition drift during recovery.
An initial broad run failed required interface comments; independent review
also found a potentially blocking fixture read and insufficient post-stop
definition checks. These were corrected, not waived. The corrected full host
race suite passed 5,319 tests/subtests across 68 packages, with 43 explicit
skips and no failures. Vet, the source build, six CLI target builds and strict
documentation checks passed. OpenCode and independent source review accepted
only the corrected, software-only scope.

The earlier native loader/offline evidence remains separate; this additional
transaction suite ran on the host, not a real Linux service manager. It does
not establish mask precedence, live process/mapped-library attestation,
firmware quiescence or hardware support. Surface testing, installed software,
existing builds and volumes were unchanged.

### Passive service observation boundary

[ADR053](../adr/adr-053-passive-sensor-service-observations.md) adds a separate
internal read-only observer, not an activation backend or installation command.
Its fixed Linux transport queries bus/name identities and the two already-loaded
service records with bounded calls and strict typed reply checks. Tool failure,
malformed output or detected identity drift is unavailable evidence, never
proof that the proxy is absent. Bracketed samples are not atomic snapshots.

The observer does not authenticate process epochs, mapped providers, vendor
definitions or policies. It cannot produce the lifecycle engine's trusted
observation or authorise any service or file effect. Scripted wire and process
tests must be reported separately from actual Linux manager integration.
The current dependency image has neither `busctl` nor systemd; no live-manager
qualification can be inferred from its earlier loader/offline results.

Host fixtures and read-only replay of a retained development archive establish
software checks only. Report separately whether actual qualification package
bytes, native relocated-loader observations and offline transactions passed.
None is a fresh native producer, live installation, service activation or
physical sensor test. Preserve
accepted source, builds and evidence while adding each new gate separately.

[ADR050](../adr/adr-050-sensor-runtime-package-plans.md) records the packaging
decision. OE owns the source inventories; Lexr owns the executable packaging
and lifecycle workflows. Live installation remains disabled.
