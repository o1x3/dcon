# Security Policy

## Reporting a vulnerability

dcon is a thin translation layer over Apple's `container` runtime — it spawns the
`container` binary and renders its output. If you find a security issue in **dcon
itself** (e.g. argument injection through flag translation, unsafe handling of
untrusted compose files, a path-traversal in `cp`/volume handling):

- Open a [private security advisory](https://github.com/o1x3/dcon/security/advisories/new), or
- Email the maintainer listed on the GitHub profile.

Please include a minimal reproduction and the output of `DCON_DEBUG=1 dcon …`
(the underlying `container` invocation). We aim to acknowledge within a few days.

## Out of scope

- Vulnerabilities in the **Apple `container`** runtime or its VMs — report those
  upstream at <https://github.com/apple/container/security>.
- Issues that require the attacker to already control your shell / the `container`
  binary on `PATH` (or `DCON_CONTAINER_BIN`).

## Supported versions

The latest released `v1.x` is supported. dcon has no network listeners and no
daemon of its own; its trust boundary is the local `container` CLI it invokes.
