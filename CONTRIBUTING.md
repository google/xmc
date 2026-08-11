
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

We use GitHub Issues to track bugs, production incidents, feature requests, and documentation improvements. When submitting an issue:

1. **Use the appropriate issue template**: Choose between [Bug Report](https://github.com/google/xmc/issues/new?template=bug_report.yml), [Incident Report](https://github.com/google/xmc/issues/new?template=incident_report.yml), or [Feature Request](https://github.com/google/xmc/issues/new?template=feature_request.yml).
2. **Provide full environment context**: Specify your XMC version / commit SHA, GKE/Kubernetes version, deployment topology (Blueprint v1 vs v2), and accelerator hardware (TPU/GPU models).
3. **Capture diagnostic logs**: Include relevant logs from the XMC controller, gateway ingress, and workload pods:
   ```bash
   # Controller logs
   kubectl logs -n xmc -l app.kubernetes.io/name=xmc-controller -c manager --tail=200

   # Workload JobSet status
   kubectl describe jobset <jobset-name> -n <namespace>
   ```
4. **Sanitize sensitive information**: Never include passwords, auth tokens, private GCP project numbers, or service account credentials in logs or YAML manifests.

### Code reviews

All submissions, including submissions by project members, require review. We
use GitHub pull requests for this purpose. Consult
[GitHub Help](https://help.github.com/articles/about-pull-requests/) for more
information on using pull requests.
