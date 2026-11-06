
# How to contribute

We'd love to accept your patches and contributions to this project.

## Before you begin

### Sign our Contributor License Agreement

Contributions to this project must be accompanied by a
[Contributor License Agreement](https://cla.developers.google.com/about) (CLA).
You (or your employer) retain the copyright to your contribution; this simply
gives us permission to use and redistribute your contributions as part of the
project.

If you or your current employer have already signed the Google CLA (even if it
was for a different project), you probably don't need to do it again.

Visit <https://cla.developers.google.com/> to see your current agreements or to
sign a new one.

### Review our community guidelines

This project follows
[Google's Open Source Community Guidelines](https://opensource.google/conduct/).

## Contribution process

### Reporting Issues & Incidents

GitHub Issues can be used to report and track bugs, production incidents, feature requests, and documentation improvements. Note that GitHub Issues is not used exclusively and does not represent an exhaustive list of all internal issues or roadmap planning. When submitting an issue:

1. **Use the appropriate issue template**: Choose between [Bug Report](https://github.com/google/xmc/issues/new?template=bug_report.yml), [Incident Report](https://github.com/google/xmc/issues/new?template=incident_report.yml), or [Feature Request](https://github.com/google/xmc/issues/new?template=feature_request.yml).
2. **Provide full environment context**: Specify your XMC version / commit SHA, GKE/Kubernetes version, deployment topology (Blueprint v1 vs v2), and accelerator hardware (TPU/GPU models).
3. **Capture diagnostic logs**: Include relevant logs from the XMC controller, gateway ingress, and workload pods:
   ```bash
   # Controller / Actuator logs
   kubectl logs -n xmc -l app=actuator -c actuator --tail=200

   # Workload JobSet status
   kubectl describe jobset <jobset-name> -n <namespace>
   ```
4. **Sanitize sensitive information**: Never include passwords, auth tokens, private GCP project numbers, or service account credentials in logs or YAML manifests.

### Pull request workflow

All changes, including changes from project members, come from feature branches
in personal forks. Do not push branches to `google/xmc` directly.

1. Fork [google/xmc](https://github.com/google/xmc) and clone your fork. With
   the GitHub CLI:
   ```bash
   gh repo fork google/xmc --clone
   cd xmc
   ```
   Without the GitHub CLI, fork the repository in the GitHub UI, clone your
   fork, and add the upstream remote:
   ```bash
   git remote add upstream https://github.com/google/xmc.git
   ```
2. Create a feature branch from the latest upstream `main`:
   ```bash
   git fetch upstream
   git switch -c <topic> upstream/main
   ```
3. Commit with a [Conventional Commits](https://www.conventionalcommits.org/)
   title, such as `fix(reconciler): ...` or `docs(readme): ...`, push the branch
   to your fork, and open a pull request against `google/xmc:main`.
4. Keep the branch current by rebasing on `upstream/main` and pushing to your
   fork. GitHub Actions checks on pull requests from forks can wait for a
   maintainer to approve the workflow run.

### Code reviews

All submissions, including submissions by project members, require review. We
use GitHub pull requests for this purpose. Consult
[GitHub Help](https://help.github.com/articles/about-pull-requests/) for more
information on using pull requests. Maintainers squash-merge approved pull
requests into `main`.
