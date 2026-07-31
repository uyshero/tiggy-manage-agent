# Core SDK release policy

The Go package `tiggy-manage-agent/sdk/tma` and the TypeScript package
`@tma/core-sdk` are two language bindings for the same Platform API contract.
They use the single version declared in `sdk/VERSION` and must be released from
the same Platform commit.

## Compatibility contract

- Patch releases are backward-compatible fixes and generated-client refreshes.
- Minor releases may add endpoints, fields, and optional behavior. Existing
  consumers must continue to compile and run.
- Major releases may remove or rename public API. They require an application
  migration guide and a supported overlap window.
- Pre-1.0 releases use the same rules operationally; an incompatible change
  increments the minor version and is called out explicitly.
- Platform supports the current SDK minor line and the immediately preceding
  minor line. Security fixes may require a newer minimum patch.

Application repositories record the consumed version in
`platform-sdk.version`, vendor the Go SDK for isolated builds, and verify a
deterministic `platform-sdk.sha256`. A release updates all three together.
The local `replace tiggy-manage-agent => ../tiggy-manage-agent` directive is
only a source for refreshing `vendor/`; CI and production builds use
`-mod=vendor`. Remove the replace after the first reachable Platform module tag
has been published.

## Release sequence

1. Regenerate OpenAPI clients and run `make ci` in Platform.
2. Tag the Platform commit with the value in `sdk/VERSION`.
3. Publish the TypeScript package with the same numeric version.
4. Refresh each application's vendor directory, version file, and checksum.
5. Run each application's `make ci` before deployment.

An application release must not consume an untagged SDK unless it is an
explicitly documented emergency build.

## TypeScript publication gate

The TypeScript package remains `private: true` until all of the following are
approved in one release change:

- package license and repository license files;
- target npm registry, package visibility, and organization ownership;
- trusted publishing/provenance configuration and release environment;
- immutable tag-to-package version verification;
- successful installation test from the published package in an isolated
  application checkout.

Until that gate closes, external applications must not use a relative path to
the Platform workspace or claim `@tma/core-sdk` as an installable dependency.
R Survival therefore keeps its declared versioned HTTP contract and migration
to the TypeScript SDK remains a release blocker, not a source-level workaround.
