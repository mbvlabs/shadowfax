# Publishing a release

Release publication is intentionally manual and must run through the Release workflow.

1. Create and push a version tag such as `v0.9.0`.
2. Open **Actions**, select **Release**, and choose **Run workflow**.
3. Enter the existing tag and start the workflow.

The workflow checks out the exact tag, builds all four targets with Go 1.26.5, creates `checksums.txt`, and verifies the Linux arm64 binary on a native arm64 runner. It uploads the files to a draft release, downloads and verifies the uploaded bytes, and only then publishes the release.

Do not create or publish the GitHub release first. The workflow refuses to modify an existing release or replace any asset.
