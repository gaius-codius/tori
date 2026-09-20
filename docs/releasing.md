# Releasing

Push a tag and publish a GitHub release for it (`gh release create v0.1.0 --generate-notes`).
The `release-assets` workflow builds the binaries, stamps the version and
attaches them with `SHA256SUMS.txt`, which is what the install script and `tori update` fetch.