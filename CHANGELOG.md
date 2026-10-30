# Changelog

## [0.2.1](https://github.com/jamesjohnsdev/calgate/compare/v0.2.0...v0.2.1) (2026-10-30)


### Features

* dockerise ([4d496c8](https://github.com/jamesjohnsdev/calgate/commit/4d496c8d8f69e7edfd3e9eacfa013444a156050b))


### Dependencies

* bump go version ([421cc84](https://github.com/jamesjohnsdev/calgate/commit/421cc84015fdb428bd8f88819e06a7aae0253e13))

## [0.2.0](https://github.com/jamesjohnsdev/calgate/compare/v0.1.0...v0.2.0) (2026-08-04)


### ⚠ BREAKING CHANGES

* **server:** server no longer stores OAuth tokens. Callers must persist and supply tokens themselves via X-Calgate-Token header.

### Features

* calgate library core + outlook read gateway ([0356c73](https://github.com/jamesjohnsdev/calgate/commit/0356c7325a0b4cdcbe06c6fb935fc9e7a96fce2e))
* **google:** add Google Calendar provider with TDD test suite ([0fa7b36](https://github.com/jamesjohnsdev/calgate/commit/0fa7b36eb8c0cfca4276b5f15b6c3f86aefbbc20))
* **server:** add HTTP microservice gateway with chi and huma ([875213e](https://github.com/jamesjohnsdev/calgate/commit/875213ebd3a92ea95e85a5a3a58682bbfe295155))
* **server:** make server stateless; move token ownership to caller ([0901d78](https://github.com/jamesjohnsdev/calgate/commit/0901d780239c456717b31b9f5fb71b365cda5024))


### Bug Fixes

* handled unchecked error ([abd71af](https://github.com/jamesjohnsdev/calgate/commit/abd71af5538c56181df05213b6c74bbbc8428542))
* **server:** resolve all golangci-lint findings ([be05a2e](https://github.com/jamesjohnsdev/calgate/commit/be05a2e03365ae3d048d74b5dfb11339397f66b2))
