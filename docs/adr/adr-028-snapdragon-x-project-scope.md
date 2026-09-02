---
id: adrs-adr028
title: "ADR028: Snapdragon X project scope"
description: Architecture Decision Record for separating Lexr's Qualcomm Snapdragon X project scope from device-specific implementation and qualification claims.
---

## Context

Lexr grew from the work required to run ARM64 Linux on a Microsoft Surface Pro
11. That device remains the maintainer's daily driver and therefore supplies
most of the project's repeatable development and physical test evidence.
Existing image adapters, device trees, kernel ABIs, release names, private
Windows hand-offs and userspace policies contain intentional SP11-specific
contracts.

The coordination and audit problems addressed by Lexr are not unique to one
Microsoft device. Other Qualcomm Snapdragon X devices need the same separation
of upstream images, model-specific boot policy, kernel and userspace provenance,
structural validation, physical qualification and private device data. Calling
Lexr a Surface Pro 11 project hides that intended collaboration boundary.
Calling every current workflow generally compatible with Snapdragon X hardware
would make a claim which the available devices and evidence do not support.

## Decision

We will describe Lexr as a Qualcomm Snapdragon X project whose current
implementation and physical qualification focus on the Microsoft Surface Pro
11. Project-level introductions and contribution guidance may address the wider
device family, while every capability and compatibility claim will retain the
narrowest scope established by its catalogue entry, adapter policy, processor
variant, device model and physical test evidence.

We will keep SP11-specific names wherever they are part of an executable,
schema, path, unit, device-tree, ABI, release, provenance, recovery or historical
decision contract. We will not infer support for one Snapdragon X device from
evidence collected on another. A new device path must add explicit policy and
reproducible evidence rather than silently broadening an existing claim.

## Consequences

- Contributors can understand that work on other Snapdragon X devices belongs
  in Lexr without mistaking that invitation for current hardware support.
- Documentation must distinguish project scope, implemented automation,
  structural validation and physical hardware qualification.
- Surface Pro 11 wording remains common in current task guides and ADRs where it
  states a tested fact or compatibility boundary.
- Some legacy names continue to use `sp11`; renaming them without a separate
  compatibility decision would damage auditability and recovery.
- Wider device coverage depends on community access to more hardware, reviewed
  model-specific policy and reproducible qualification results.
