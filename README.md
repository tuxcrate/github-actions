# github-actions

Shared container build and release workflows for Tuxcrate.

[Documentation](https://github.com/tuxcrate/docs/blob/main/docs/maintainers/container-releases.md)

## Shared runtime-user validation

Before running a repository's `tests/smoke.sh`, `container-artifact test` checks
every platform image with its default user, no network, and `/bin/sh` as the
entrypoint. Images must provide `/bin/sh` and `id`. The default requirement is
user and group `tuxcrate`, both with numeric ID 1000. The check also runs when
there is no repository smoke hook.

Images that require root must explicitly declare
`LABEL com.tuxcrate.runtime-user="root"` and document why. This checks for
`0:0:root:root`; it does not skip user validation. The optional value `tuxcrate`
declares the default explicitly; all other values fail validation.

Keep directory permissions, application startup, and API checks in repository
smoke hooks. Deploy root-user declarations before enabling the shared validator,
and deploy the validator before removing duplicate checks from callers.
