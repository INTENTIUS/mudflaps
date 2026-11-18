# Releasing

mudflaps releases with [`chant release`](https://intentius.io/chant/cli/release/).
A release is started by hand: the [Release workflow](.github/workflows/publish.yml)
picks the newest green commit of `main`, cuts it as `vX.Y.Z`, and publishes:

- a GitHub Release `vX.Y.Z` with the GoReleaser archives for linux, darwin and
  windows on amd64 and arm64 (`.tar.gz`, `.zip` on windows) and `checksums.txt`,
  with notes generated from the conventional commits since the previous release;
- the multi-arch (linux/amd64, linux/arm64) container image
  `ghcr.io/intentius/mudflaps:X.Y.Z`, with `latest` moved to it and `X.Y.Z-amd64` and `X.Y.Z-arm64` for each platform.

The release is configured in [`chant.config.json`](chant.config.json). The cut
runs GoReleaser in snapshot mode (`.goreleaser.yaml`) for the binaries, archives
and checksums, builds the image from the top-level `Dockerfile`, records every
file by digest, and pushes the tag. The publish job then uploads exactly those
files and that image. Tags stay `vX.Y.Z`; the `v*` tags from before chant are
read as the release history.

`publish.yml` is generated: change `chant.config.json`, then regenerate it with

```sh
npx --yes @intentius/chant@0.124.107 release workflow --force
```

## Steps

1. **Update the changelog.** In `CHANGELOG.md`, move the `## [Unreleased]` items
   into a new `## [X.Y.Z] - YYYY-MM-DD` section and refresh the link references at
   the bottom. Commit it to `main` (via PR) and let CI go green.

2. **Check the label** (optional). From a clean, up-to-date `main`:

   ```sh
   npx --yes @intentius/chant@0.124.107 release next     # the label the commits give
   ```

3. **Start the release:**

   ```sh
   just release X.Y.Z      # or: make release V=X.Y.Z
   ```

   The recipe refuses to proceed unless the working tree is clean, you are on
   `main`, the changelog has a `## [X.Y.Z]` section, and `build`/`vet`/`gofmt`/
   `test` all pass. It then dispatches the Release workflow with the label
   `X.Y.Z`. `gh workflow run publish.yml -f label=auto` takes the label from the
   commits instead.

4. **Watch the release build:**

   ```sh
   gh run watch --repo INTENTIUS/mudflaps
   ```

   When it's green, confirm the [GitHub Release](https://github.com/INTENTIUS/mudflaps/releases)
   has its assets and the image is pullable:

   ```sh
   docker pull ghcr.io/intentius/mudflaps:X.Y.Z
   ```

## First-release-only

A newly created GHCR container package is **private** by default. If
`docker pull` returns `unauthorized`, make the package public once:
**org → Packages → `mudflaps` → Package settings → Change visibility → Public**.
This is a one-time setting; later releases inherit it.

## Versioning

mudflaps follows [semantic versioning](https://semver.org). While pre-1.0, minor
bumps carry new endpoints / behavior and patch bumps carry fixes.
