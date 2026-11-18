# Discovery fixtures

Extracted from built production SPKs, without regenerating the gateway JSON.
No credentials or machine addresses are included. SDK versions come from the
SPK-root `sdk-package.json` native_gateway.sdk inventory. The gateway JSON in
these releases has no SDK version field; discovery diagnoses ping support instead of inferring a version.
The absent external object disables public features and defaults to optional.

- SynoPkgJellyfin-12.2-0006-x86_64.spk: SDK 0.26.0
  - SPK SHA-256: `726153dbebb53fc222369d16ab00bce103d229e8c4d13d87df7bbf1ef974188f`
  - gateway.json SHA-256: `23b37aa2f523a1092e5c7208cf5d4ed754cb2140b10c44b51202fc7f62ac0c27`
- SynoPkgOpenList-4.2.7-0017-x86_64.spk: SDK 0.27.0
  - SPK SHA-256: `2003ce6976c8d266bc3dcb85e321d613ae5b7104966ba5a523545968a857e0c1`
  - gateway.json SHA-256: `924d5510f3fe3df5b6b13bc4460456e76647e0e176d60e71d483e2d62bf1bf80`
