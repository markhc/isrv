# Changelog

## [1.5.0](https://github.com/markhc/isrv/compare/v1.4.0...v1.5.0) (2026-07-11)


### Features

* add cluster mode ([#61](https://github.com/markhc/isrv/issues/61)) ([bbd2c76](https://github.com/markhc/isrv/commit/bbd2c76327aacea1bc2d8cb934c1fa7dfea253f9))
* **process:** enable systemd service ([#54](https://github.com/markhc/isrv/issues/54)) ([8a47c83](https://github.com/markhc/isrv/commit/8a47c83f4dacf5cccac82e9d9c94a42b626e93e7))

## [1.4.0](https://github.com/markhc/isrv/compare/v1.3.0...v1.4.0) (2026-07-08)


### Features

* **admin:** rate limit failed login attempts ([#46](https://github.com/markhc/isrv/issues/46)) ([29eb160](https://github.com/markhc/isrv/commit/29eb16081eb34556a9bd576a1faaa0d3403a4cd9))
* **logs:** adds anonymize mode ([#49](https://github.com/markhc/isrv/issues/49)) ([c0043d5](https://github.com/markhc/isrv/commit/c0043d5fe38253d8c90cc248985f0a3e5555b3ec))
* **storage:** add gcs backend ([#52](https://github.com/markhc/isrv/issues/52)) ([5979e19](https://github.com/markhc/isrv/commit/5979e19ea687e9c894043f83e468b8ad15e3052d))


### Bug Fixes

* **config:** seed with default values before parsing ([#47](https://github.com/markhc/isrv/issues/47)) ([47d84e5](https://github.com/markhc/isrv/commit/47d84e5d343ba3c63beacd505f04cb5fbe3de33b))

## [1.3.0](https://github.com/markhc/isrv/compare/v1.2.1...v1.3.0) (2026-07-06)


### Features

* add encryption system ([#45](https://github.com/markhc/isrv/issues/45)) ([db863cb](https://github.com/markhc/isrv/commit/db863cb007f931e00377a0c514615141ba527c57))


### Code Refactoring

* **storage:** decouple file serving from storage drivers ([#43](https://github.com/markhc/isrv/issues/43)) ([ada5158](https://github.com/markhc/isrv/commit/ada515815c4ec37acf2bb018f95eb06aee17a6d1))

## [1.2.1](https://github.com/markhc/isrv/compare/v1.2.0...v1.2.1) (2026-07-05)


### Bug Fixes

* add link to repo on the about page ([#39](https://github.com/markhc/isrv/issues/39)) ([ce99eca](https://github.com/markhc/isrv/commit/ce99ecabada1abbf1203fe74b342c125ce25c34d))

## [1.2.0](https://github.com/markhc/isrv/compare/v1.1.0...v1.2.0) (2026-07-05)


### Features

* add renovate bot ([#19](https://github.com/markhc/isrv/issues/19)) ([34fcc72](https://github.com/markhc/isrv/commit/34fcc7224070ec68b654eab5b63234f7b59c1b3e))
* adds renovate workflow ([#22](https://github.com/markhc/isrv/issues/22)) ([4c78a7d](https://github.com/markhc/isrv/commit/4c78a7dcd55fc06ec64210afc11bf16d3360bcb1))
* proxy downloads; multipart upload to s3 ([#34](https://github.com/markhc/isrv/issues/34)) ([a2857c6](https://github.com/markhc/isrv/commit/a2857c6ddb0dda0ea075f7822fa045308bf92a64))


### Bug Fixes

* correct 6% failure rate of tampered token test ([#29](https://github.com/markhc/isrv/issues/29)) ([6b297fb](https://github.com/markhc/isrv/commit/6b297fb41fe5352ce79bed7ebbdb2a3075fc6c6b))
