# Documentation assets

## `setup-demo.gif`

The root README's setup animation is a sanitized visual replay of an executed
Windows `386` foreground run against the operator-supplied Graybox installation
recorded in [`docs/compatibility.md`](../compatibility.md). The live run
confirmed guided selection, HTTP startup, and a connected status response
before the animation was rendered.

The GIF is orientation, not compatibility evidence. It deliberately omits
timestamps, machine paths, process values, and the complete status payload.
The adjacent README steps are the accessible and copyable source of truth.

When replacing it:

- execute the depicted flow against an authorized local test source;
- keep Write disabled and use foreground loopback execution;
- do not show process values, credentials, private ItemIDs, or machine-specific
  paths;
- preserve the matching text walkthrough and descriptive alt text;
- keep the asset at or below 960×540 and approximately 2 MiB;
- update compatibility evidence separately when the run establishes a new
  compatibility result.
