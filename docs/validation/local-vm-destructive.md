# Local Windows VM destructive review

## Execution status

**IN PROGRESS — no local-VM PASS result is recorded yet.**

GitHub-hosted runner results do not satisfy this procedure. Evidence is valid
only when produced by the dedicated local KVM/libvirt VM named
`opcda-destructive-review` and recorded here with its base-image identity,
adapter commit, fixture commit, architecture, and resource observations.

## Preparation evidence (not a PASS result)

Observed locally on 2026-08-24 (Asia/Seoul):

- Microsoft URL: `https://aka.ms/WinServ2025vhd-enus`
- downloaded VHDX size: `11,686,379,520` bytes
- downloaded VHDX SHA-256:
  `2d175924c8e647969a82e36f931b22397108bd94a030e0e947b7e66e47e0be9a`
- `qemu-img check` result: no VHDX errors; virtual size 64 GiB
- immutable converted qcow2 SHA-256:
  `fda55996b3958e2e632e4584d80d65edf281c3c4221e937d7614d8fbee7bd0b0`
- VM: `opcda-destructive-review`, UUID
  `7b56cf9b-5d87-4027-aa2a-468339e61957`, Q35/UEFI, KVM, 4 vCPU,
  10 GiB RAM
- dedicated network: `opcda-review-net`, UUID
  `beadcfb5-1014-4037-8e6c-0e37b6a55f29`, NAT
  `192.168.231.0/24`; it is not shared with either unrelated local VM
- initial guest: Windows Server 2025 Datacenter Evaluation, 64-bit, build
  `26100`; WinRM Basic and `AllowUnencrypted` are false and its firewall
  remote address is only `192.168.231.1`
- protected `main` baseline: `ba256bda31c5356da8f4c70c63890994cb005771`
- locally reproduced dirty validation executables:
  - windows/386:
    `b42fa2cf2d13e5cdf6e8e7ea5f412c32d000d147e7091195261bc5b6f566cbc4`
  - windows/amd64:
    `7316f3e0249566839eb63f64f9e2fa296528b330069844ebae38e44d9f3c116f`
- pinned fixture source: `OPCF-Members/OPC-Classic-CoreComponents` commit
  `efe0d1d1ea86a8a727bf26a501a261765e836766`; archive SHA-256
  `ac886fa4be0db4f880aac4981dabf84cb14a45eff7b11ee1bcac5fd9a1b4728f`

Windows security updates, current Defender signatures, full Defender scan,
source build, test execution, reboot, and cleanup are still in progress. None
of the preparation observations above is recorded as scenario PASS.

## Isolation boundary

- Use a dedicated Windows Server evaluation VM on libvirt's NAT network.
- Do not start, modify, snapshot, or attach disks from unrelated local VMs.
- Keep the downloaded Microsoft base image immutable and run tests in a
  disposable copy-on-write disk.
- Create a clean snapshot after OS updates and build-tool installation.
- Build the pinned OPC Foundation fixture from source inside this VM, scan
  source and outputs with Defender, and never install a proprietary simulator.
- Store no real process values, production credentials, or vendor files in the
  VM.
- Restore all AppID values and COM registrations in `finally` cleanup, then
  revert or destroy the disposable test overlay.

## Required destructive matrix

| Area | Required observation |
|---|---|
| Normal | Connect, nested Browse, ordered partial Read, disabled and typed Write |
| Invalid input | malformed/truncated/oversized JSON, invalid UTF-8, NUL, excessive depth and batch sizes |
| Intentional HTTP abuse | slow headers/bodies, connection exhaustion, rapid requests, concurrency above all configured bounds, recovery after load |
| Source failure | kill and unregister the DA server during Reads and Writes; no stale Good value and no Write replay |
| Adapter failure | force-kill and restart the adapter repeatedly; COM registrations and source process remain bounded |
| Reboot | reboot the VM with registered fixture state, then confirm deterministic startup/cleanup and no persisted process value |
| DCOM/COM permissions | deny local Launch/Activation, observe exact HRESULT, restore the application ACL, and recover without weakening machine defaults |
| Identity | exercise an allowed standard local account and a denied account where the fixture supports it; never grant remote rights |
| Architecture | validate separate x86/386 and x64/amd64 registry views, proxies, fixture, and adapter |
| Resource bounds | monitor handles, private bytes, process count, queue depth, reconnect count, and event-log errors under soak |
| Security | Defender scans, no unexpected listener, default loopback isolation, no process values in adapter output |

The permission scenarios must snapshot the exact AppID values before mutation
and restore absence as absence, not as a broad replacement ACL. A test failure
must still execute cleanup. Machine-wide COM defaults and the unrelated Ubuntu
VM are never test targets.

## Evidence record

The completed record must include:

- Microsoft evaluation image URL, file size, and SHA-256 observed locally;
- Windows build and installed update level;
- VM definition, network type, snapshot name, and test time window;
- adapter commit and both executable hashes;
- pinned fixture commit and source/output Defender results;
- exact scenario counts and PASS/FAIL/BLOCKED per architecture;
- bounded resource deltas and relevant DCOM event IDs;
- cleanup and snapshot-revert result.

Do not promote a release solely from a workflow result. A failing or incomplete
row remains explicit and blocks the local destructive-validation gate.
