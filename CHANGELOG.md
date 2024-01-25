# Changelog

### [0.12.3](https://www.github.com/werf/kubedog/compare/v0.12.2...v0.12.3) (2024-01-25)


### Bug Fixes

* dynamic tracker hotfixes ([3817e3d](https://www.github.com/werf/kubedog/commit/3817e3dcea49144187798b37ba7a1962de3708bf))

### [0.12.2](https://www.github.com/werf/kubedog/compare/v0.12.1...v0.12.2) (2024-01-22)


### Bug Fixes

* bump version ([1e49622](https://www.github.com/werf/kubedog/commit/1e49622b178802e6bdded01ce48c979e6812ffbe))

### [0.12.1](https://www.github.com/werf/kubedog/compare/v0.12.0...v0.12.1) (2024-01-22)


### Bug Fixes

* switch to go 1.21 ([6cc5b83](https://www.github.com/werf/kubedog/commit/6cc5b832e653650441ba9aa4e1892baf22164272))
* update all modules ([5b3cd82](https://www.github.com/werf/kubedog/commit/5b3cd82edcc89ff05ae790461e9ddd7c0d71bd1a))

## [0.12.0](https://www.github.com/werf/kubedog/compare/v0.11.0...v0.12.0) (2023-12-28)


### Features

* **dynamic:** expose more Attributes, add more options for Tracker ([51dbaa7](https://www.github.com/werf/kubedog/commit/51dbaa72362109c353877d3ed2a688e7274ce081))

## [0.11.0](https://www.github.com/werf/kubedog/compare/v0.10.0...v0.11.0) (2023-12-18)


### Features

* add a lot of new generic conditions for generic tracker ([8c44e5d](https://www.github.com/werf/kubedog/commit/8c44e5d6ff6ff3a0693d1fcbc4d748ea3382a5cf))
* add acid.zalan.do/postrgresql generic tracker rule ([d7d8648](https://www.github.com/werf/kubedog/commit/d7d86482772583645f6022d16956260b9b034c75))
* add more status conditions, add extra case options for conditions ([969cad9](https://www.github.com/werf/kubedog/commit/969cad97bf11a1c73211c41b7133cb057334a0a5))
* separate file for user-contributed resource status rules ([0f41256](https://www.github.com/werf/kubedog/commit/0f41256c6a63d3a3d490e64e21e34c734d59c2c3))
* track status.(current)status in generic tracker ([03ed5b2](https://www.github.com/werf/kubedog/commit/03ed5b2ddf87a2474fba92502f741b2ac870367b))


### Bug Fixes

* condition column name ([f7bed28](https://www.github.com/werf/kubedog/commit/f7bed28512312f7f9bdde4d8f3d957a873183bb8))
* preserve original case for values when building generic status ruleset ([869b5fe](https://www.github.com/werf/kubedog/commit/869b5fee19a6cff6f31204e85fae73d865fa0708))
* refactor generic ruleset generation ([7560fd2](https://www.github.com/werf/kubedog/commit/7560fd25ceb8ccff52811da169c095cfdad00ab3))
* refactor generic status rules /2 ([855ef9d](https://www.github.com/werf/kubedog/commit/855ef9dcf1810ad18929825a8102920a25eba6d0))
* refactor generic status rules /3 ([a8eeb67](https://www.github.com/werf/kubedog/commit/a8eeb674f64492a02d9673c8bcb5178617679d90))

## [0.10.0](https://www.github.com/werf/kubedog/compare/v0.9.12...v0.10.0) (2023-12-15)


### Features

* new high-level concurrent dynamic tracker ([5721c3e](https://www.github.com/werf/kubedog/commit/5721c3ed54d4bd26a53743e3e6028bd85015ad6a))

### [0.9.12](https://www.github.com/werf/kubedog/compare/v0.9.11...v0.9.12) (2023-05-29)


### Bug Fixes

* resource hangs on context canceled ([0c195e2](https://www.github.com/werf/kubedog/commit/0c195e2f8a6b297e1afbc622f6dec05dffe039e6))

### [0.9.11](https://www.github.com/werf/kubedog/compare/v0.9.10...v0.9.11) (2023-03-17)


### Bug Fixes

* **deps:** update logboek ([f4b0ab7](https://www.github.com/werf/kubedog/commit/f4b0ab7a3f042ba2fd97727ad443b7e2bb5d9a44))

### [0.9.10](https://www.github.com/werf/kubedog/compare/v0.9.9...v0.9.10) (2023-03-13)


### Bug Fixes

* update dependencies ([7ccd3cb](https://www.github.com/werf/kubedog/commit/7ccd3cb56bb44179befc66d957f6bec6e56fb237))

### [0.9.9](https://www.github.com/werf/kubedog/compare/v0.9.8...v0.9.9) (2023-03-09)


### Bug Fixes

* **ci:** update linter ([140b339](https://www.github.com/werf/kubedog/commit/140b33932d952f43e9972680cc39141367147bb1))

### [0.9.8](https://www.github.com/werf/kubedog/compare/v0.9.7...v0.9.8) (2023-03-09)


### Bug Fixes

* update to Go 1.20 ([37db5ec](https://www.github.com/werf/kubedog/commit/37db5ec4ce03fc01d20e8930f1a709349805809d))

### [0.9.7](https://www.github.com/werf/kubedog/compare/v0.9.6...v0.9.7) (2023-03-09)


### Bug Fixes

* trigger release ([2421e8b](https://www.github.com/werf/kubedog/commit/2421e8b9c5f84f7b54e8c50b38b96d50933f67b8))

### [0.9.6](https://www.github.com/werf/kubedog/compare/v0.9.5...v0.9.6) (2022-07-29)


### Bug Fixes

* **generic:** ignore jsonpath errs on Condition search ([8d88c65](https://www.github.com/werf/kubedog/commit/8d88c6509e3ac1c12a8a564aebb9e04d2b7c73e0))

### [0.9.5](https://www.github.com/werf/kubedog/compare/v0.9.4...v0.9.5) (2022-07-26)


### Bug Fixes

* **generic:** add logging and don't retry fatal errors on List/Watch ([246d454](https://www.github.com/werf/kubedog/commit/246d45452ae7686584d67dfa4763bf6563907a30))
* **generic:** Condition output was malformed ([8c05e40](https://www.github.com/werf/kubedog/commit/8c05e40d9a5381c88b38982d284e6d4f8653d917))
* hide Header if no resources of such type being tracked ([232c4ed](https://www.github.com/werf/kubedog/commit/232c4ede20fa52f18a2e574c173b94e6d0a114cd))

### [0.9.4](https://www.github.com/werf/kubedog/compare/v0.9.3...v0.9.4) (2022-07-21)


### Bug Fixes

* **generic-tracker:** improve logging + few possible fixes ([3524520](https://www.github.com/werf/kubedog/commit/352452071afd55b57ef721b8b271e9acc9849c75))

### [0.9.3](https://www.github.com/werf/kubedog/compare/v0.9.2...v0.9.3) (2022-07-20)


### Bug Fixes

* Generic tracker hangs if no list/watch access ([946d650](https://www.github.com/werf/kubedog/commit/946d650746a249a92c0cdbc241958ecb519c8a88))

### [0.9.2](https://www.github.com/werf/kubedog/compare/v0.9.1...v0.9.2) (2022-07-19)


### Bug Fixes

* improve generic tracker output ([60602b0](https://www.github.com/werf/kubedog/commit/60602b05cc942cd27ef5354be15c2d744a2e5092))

### [0.9.1](https://www.github.com/werf/kubedog/compare/v0.9.0...v0.9.1) (2022-07-18)


### Bug Fixes

* increase noActivityTimeout from 1.5 to 4min ([00b49d8](https://www.github.com/werf/kubedog/commit/00b49d814dc0d807e374967fc19ce9d38c9dde28))
* reword no activity error message ([e4b1302](https://www.github.com/werf/kubedog/commit/e4b13020cca2f0a175c51316945dd478b79d4d9a))

## [0.9.0](https://www.github.com/werf/kubedog/compare/v0.8.0...v0.9.0) (2022-07-18)


### Features

* improved Generic progress status ([5f68bca](https://www.github.com/werf/kubedog/commit/5f68bca131024ed5a5b791f3194f98e3304e5b16))


### Bug Fixes

* job duration stops changing ([4aa62c3](https://www.github.com/werf/kubedog/commit/4aa62c3bc21778b4fd2aff2c7b28432d54a3524c))

## [0.8.0](https://www.github.com/werf/kubedog/compare/v0.7.1...v0.8.0) (2022-07-15)


### Features

* show Ready resources only once ([322a781](https://www.github.com/werf/kubedog/commit/322a781e52bb75be2ab39c2bc22ff1ab091c39dd))

### [0.7.1](https://www.github.com/werf/kubedog/compare/v0.7.0...v0.7.1) (2022-07-06)


### Bug Fixes

* non-blocking mode doesn't work ([71e8826](https://www.github.com/werf/kubedog/commit/71e88261b930965dd473af7274b0ec3f9dd7e9ba))

## [0.7.0](https://www.github.com/werf/kubedog/compare/v0.6.4...v0.7.0) (2022-07-05)


### Features

* generic resources tracking ([ba88553](https://www.github.com/werf/kubedog/commit/ba88553162024253f8d00be930931ebca0975b07))


### Bug Fixes

* **kube:** do not use memcache discovery client for base64 kubeconfig ([d1cd71b](https://www.github.com/werf/kubedog/commit/d1cd71bd4f07f0913acb7c2bfdee72ba865cf9a0))
* **kube:** fix GetAllContextsClients not working in in-cluster mode ([802c1b0](https://www.github.com/werf/kubedog/commit/802c1b0fd9afde8ca41eeee7719f0ddb0a4f9dfd))

### [0.6.4](https://www.github.com/werf/kubedog/compare/v0.6.3...v0.6.4) (2022-02-22)


### Bug Fixes

* **kube-client:** support kube config merge list option for KubeClientGetter ([ae4dd95](https://www.github.com/werf/kubedog/commit/ae4dd95bf6e7df5ca850a81dd6078dc801217242))

### [0.6.3](https://www.github.com/werf/kubedog/compare/v0.6.2...v0.6.3) (2022-02-07)


### Bug Fixes

* **elimination:** fixed possible race-condition which could result in haning elimination tracker ([8007d2e](https://www.github.com/werf/kubedog/commit/8007d2ebfcda7ace85fa43f77b24e0d2b63114ac))
* **elimination:** refactor elimination tracker, fix "panic: close of closed channel" ([8a2f13e](https://www.github.com/werf/kubedog/commit/8a2f13ef93de699ce1225d6aa2824e4b91ec19db))
* **kube:** fix kube client ignores KUBECONFIG ([f7d600a](https://www.github.com/werf/kubedog/commit/f7d600a51cbcb3fdf9df8f11028b4888ac4d61fe))
* trigger release ([6163bc9](https://www.github.com/werf/kubedog/commit/6163bc9d2a5f09e1353a1c88cc869c1a7d41392c))

### [0.6.2](https://www.github.com/werf/kubedog/compare/v0.6.1...v0.6.2) (2021-09-16)


### Bug Fixes

* correction release ([da7c662](https://www.github.com/werf/kubedog/commit/da7c6620158ebbb5e0bd3b7026173517ec38900c))

### [0.6.1](https://www.github.com/werf/kubedog/compare/v0.6.0...v0.6.1) (2021-09-09)


### Bug Fixes

* **kube-client:** support KUBECONFIG-like list of config paths ([4433815](https://www.github.com/werf/kubedog/commit/44338155c27b2c25963aea72123f3dea2045c572))
