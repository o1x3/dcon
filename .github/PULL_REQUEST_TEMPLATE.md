<!-- Thanks for contributing to dcon! -->

## What & why

<!-- What does this change, and why? Link any issue: Fixes #123 -->

## Type

- [ ] feat — new command/flag or behaviour
- [ ] fix — bug / parity fix
- [ ] docs
- [ ] test / ci / chore

## Checklist

- [ ] `make test` passes (including `TestNoFlagShorthandCollisions`)
- [ ] `make vet fmt` clean
- [ ] Flag translation lives in a testable function with a test in `cmd/translate_test.go`
- [ ] Docs/parity tables updated if the command surface changed
- [ ] Honest about any approximation (no claimed parity dcon doesn't have)
