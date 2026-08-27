# ADR-0017: a third-party vendor DA server for validation

- Status: **Proposed — not decided, and nothing is executed under it**
- Date: 2026-08-27
- Relates to: [ADR-0006](0006-real-da-validation-fixture.md)

## Context

Since this was written, a second reason has appeared. OPC 10000-8 Table A.1 is
implemented and **cannot be exercised against the pinned fixture at all**: its
configuration defines only Scan Rate on its items, and Table A.1 maps none of
properties 1 to 8 onto a UA property. `docs/compatibility.md` records the
observation and how it was attributed. A vendor server with engineering units
and ranges is the only thing that can validate that mapping.


Every real-DA result this project has is against one server: the OPC Foundation
DA 2.05a test server that ADR-0006 builds from pinned source. `docs/compatibility.md`
says so on every result, and the gap has been open since the first one. A second
server is the only thing that can distinguish "the adapter works" from "the
adapter works against the server it was written beside", which is the same
distinction the third-party OPC UA clients drew when they found six defects in a
frontend that had only ever talked to itself.

ADR-0006 already considered this and declined:

> Proprietary freeware simulators were rejected because unattended install would
> require accepting vendor license terms.

That reason was examined again on 2026-08-27 against a specific candidate, and
it does not hold for that candidate. What blocks it is something else.

## The candidate

Graybox **Gray Simulator**, by Complex Systems RDC / Graybox Software, described
by the OPC Training Institute as compliant with OPC Data Access 1.00, **2.05a**
and 3.00 — the version this adapter targets.

Its stated terms are the reason to look at it:

> Gray Simulator is freeware. You can download and redistribute it with your
> products **without any license limitations**.

If there is no licence to accept, ADR-0006's stated objection is answered.

## What actually blocks it

**1. The vendor's own distribution is gone.** `gray-box.net`, the domain the
vendor published from, now serves an unrelated commercial site. There is no
first-party download, so there is nothing to compare a copy against and no
vendor-published checksum or signature to verify one with. This is the opposite
of ADR-0006's fixture, whose provenance is a signed upstream commit.

**2. The surviving copy is behind an account.** The OPC Training Institute
mirrors it, but its download is an ASP.NET postback that answers with:

> One-Time Registration Process … OPCTI will send you an email with a link to
> verify your contact details.

So it cannot be fetched unattended by CI, and obtaining it means somebody
submitting their own contact details to a third party. That is a decision for
the person whose details they are, not for an automated agent.

**3. It is a pre-built binary.** ADR-0006's fixture is compiled from audited
source with no installer, no `FetchContent`, and no download step. A vendor
binary is a categorically weaker provenance, and registering one gives it COM
registration privileges on the validation host.

## Options

**A. Do nothing.** The gap stays open and stays disclosed. Costs nothing, proves
nothing new. This is the current state.

**B. Operator-supplied, pinned artefact.** The maintainer obtains the installer
themselves, records its SHA-256, and publishes it where CI can fetch it
unattended. The licence explicitly permits that redistribution. CI then treats
it exactly as ADR-0006 treats its fixture: pinned by digest, Defender-scanned
before and after, registered only on the ephemeral runner, unregistered in a
`finally`, never shipped with the adapter, and never allowed to run as a
service. The residual risk is that the pinned digest attests to *a* copy, not to
an authentic one, because no first-party original survives to compare against.

**C. One manual run, recorded.** The maintainer installs it on their own Windows
machine, runs the existing validation probes against it once, and the exact
observations go into `docs/compatibility.md` as a dated one-off. No binary enters
CI or the repository. This is the smallest supply-chain step and yields a real
vendor observation, but it is not repeatable and will not catch a regression.

## Decision

**None yet.** This ADR exists so the choice is made deliberately rather than
drifting, and so the next person does not re-derive that the licence is fine and
the provenance is not.

If B or C is chosen, the results must be recorded the way every other result in
this project is: as evidence about **one** named vendor server at a named
version, never as vendor-wide compatibility. `docs/compatibility.md` already
lists the vendor variations such a run should look for — a source without an
`IOPCDataCallback` connection point, an unrecognised disconnect HRESULT, a
canonical type or access rights reported differently, a revised update rate far
from the requested one.

## Consequences

Until this is decided, "no third-party vendor DA server has been tested" remains
true and remains stated wherever a real-DA result appears. Choosing A leaves that
permanent. Choosing B or C narrows it to one vendor and no further.
