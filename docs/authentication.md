# Authentication and Experiment Access

Every call from an XMC client, such as the XManager CLI, the example launchers, or `xdash`, carries an identity token. XMC first checks that the identity is admitted to the deployment, then uses the token's email as your identity: it records you as the creator of what you create and checks experiment ACLs against you. The email always comes from the token. Setting `XMC_USER_EMAIL` doesn't change who you are to the server.

This page covers how to get a token as a person and as a CI job, and how to control who can see your experiments.

## Who Can Use a Deployment

A deployment admits an identity if one of these is true, checked in this order:

1. Its email is in `allowed_emails` (Helm `auth.allowedEmails`).
2. Its email domain is in Helm `auth.allowedDomains`.
3. It holds the `authorized_iam_role` (Helm `auth.authorizedIamRole`) on the project through a direct binding. Holding owner or editor, or getting the role through a group, doesn't count.

The check applies to the web UI, to `xmanager login`, and to every gRPC request. See the [Configuration Reference](../INSTALL.md#configuration-reference) for how to set these values.

Emails, domains, and IAM members match the same way as [ACL principals](#experiment-access-acls): surrounding whitespace is ignored and only ASCII letters are case-insensitive, so `Alice@Example.com` matches `alice@example.com`, but a non-ASCII look-alike such as the Kelvin sign (U+212A) never matches `k`. Sign-in through the Dashboard or CLI login also requires a [plain ASCII email address](#experiment-access-acls); other identities are not admitted.

## Interactive Use: `xmanager login`

Sign in as yourself with the `xmanager` CLI (requires `xmanager >= 0.8.5.post1` and XMC `>= 0.16.0`), installed via `pip install "xmanager>=0.8.5.post1"` or [`install_xmanager.py`](../deploy/cli/README.md):

```bash
xmanager login   # Reads XMANAGER_ENDPOINT, XMC_API_URL, XMANAGER_UI_URL, or XMC_DOMAIN from the environment.
```

`xmanager login` caches a per-deployment session token in `$XMC_CREDENTIALS_FILE` or `$XDG_CONFIG_HOME/xmc/credentials.json` (default `~/.config/xmc/credentials.json`, mode `0600`). The XManager CLI (`xm_cloud`) loads this cached token automatically — once you're signed in, experiment and work unit calls don't need `XMC_AUTH_TOKEN`, `IAP_CLIENT_ID`, or `XMC_CLIENT_SA`, and experiments you create are attributed to you.

- Tokens last 12 hours by default. When yours expires, run `xmanager login` again.
- Run `xmanager login --status` to see your account and expiry. `xmanager logout` removes the cached token for the current deployment (`xmanager logout --all` clears all deployments).
- Over SSH or on a headless machine, `xmanager login` switches to a copy-and-paste flow on its own (or pass `--no-browser` explicitly).
- For [`xdash`](../sdks/python/xdash/README.md) and the dashboard-creating examples ([`chart_artifact_job`](../examples/chart_artifact_job/), [`clickhouse_metrics_job`](../examples/clickhouse_metrics_job/), [`metrics_dashboard_job`](../examples/metrics_dashboard_job/)), which read `XMC_AUTH_TOKEN` directly for `DashboardService` calls, also run `export XMC_AUTH_TOKEN=$(xmanager login --print-token)` in your shell.

## CI and Other Non-Interactive Use: A Service Account

Jobs that can't open a browser authenticate as a service account. Mint an ID token for it and export the token:

```bash
export XMC_CLIENT_SA="ci-runner@your-gcp-project-id.iam.gserviceaccount.com"
export XMC_AUTH_TOKEN="$(gcloud auth print-identity-token \
  --impersonate-service-account="${XMC_CLIENT_SA}" \
  --audiences="${IAP_CLIENT_ID}" \
  --include-email)"
```

If `XMC_AUTH_TOKEN` is unset, the XManager CLI impersonates `XMC_CLIENT_SA` itself and uses `IAP_CLIENT_ID` as the audience. Either way, the caller needs permission to mint tokens for the service account, for example `roles/iam.serviceAccountTokenCreator` on it.

> [!IMPORTANT]
> - **The service account must be admitted.** Add its email to `allowed_emails`, or grant it the `authorized_iam_role` through a direct binding. Otherwise every request is rejected.
> - **Its experiments belong to the service account, not to you.** The service account is the creator and, by default, the only owner. To let people see them, grant those people access explicitly (see [Experiment Access](#experiment-access-acls)).
>
> Don't share one service account among people for interactive work. Use `xmanager login` so each experiment is attributed to the person who ran it.

## Experiment Access (ACLs)

Each experiment has an ACL with three roles. Each role includes the ones below it:

| Role | Allows |
| :--- | :--- |
| `owners` | Everything a writer can do, plus deleting. |
| `writers` | Adding and changing work units, artifacts, and metadata. |
| `readers` | Viewing the experiment in the UI and through the API. |

A role can name a `user`, a `group`, a `service_account`, a `domain`, or `all_authenticated_users`.

**Matching:** principals match case-insensitively for ASCII letters, so `Alice@Example.com` and `alice@example.com` name the same user. Authenticated emails must be plain ASCII addresses: letters, digits, and `.` `_` `%` `+` `'` `-` before the `@`, with a dotted domain. XMC rejects requests from identities whose email doesn't conform, for example one with a display name, surrounding whitespace, or non-ASCII characters. XMC stores ACL principals trimmed and lowercased. Group-based access is checked with Cloud Identity using your authenticated email, so it also requires a conforming ASCII email address.

**Default:** if you create an experiment without ACLs, XMC makes you, the creator, its only owner. The [examples](../examples/) rely on this default.

**Sharing:** pass ACLs to share an experiment. They must grant you at least write access, or XMC rejects the request. List yourself, or a group you belong to, as an owner:

```python
from xmanager import xm_cloud

Role = xm_cloud.Acls.Role

with xm_cloud.create_experiment(
    experiment_title="Shared experiment",
    acls=xm_cloud.Acls(
        owners=[Role(user="you@example.com")],
        readers=[Role(group="ml-team@example.com")],
    ),
) as experiment:
    ...
```

> [!CAUTION]
> `all_authenticated_users` grants the role to everyone admitted to the deployment. Use it only for experiments you mean to share that widely.
