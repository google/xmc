# Authentication and Experiment Access

Every call from an XMC client, such as the XManager CLI, the example launchers, or `xdash`, carries an identity token. XMC first checks that the identity is admitted to the deployment, then uses the token's email as your identity: it records you as the creator of what you create and checks experiment ACLs against you. The email always comes from the token. Setting `XMC_USER_EMAIL` doesn't change who you are to the server.

This page covers how to get a token as a person and as a CI job, and how to control who can see your experiments.

## Who Can Use a Deployment

A deployment admits an identity if one of these is true, checked in this order:

1. Its email is in `allowed_emails` (Helm `auth.allowedEmails`).
2. Its email domain is in Helm `auth.allowedDomains`.
3. It holds the `authorized_iam_role` (Helm `auth.authorizedIamRole`) on the project through a direct binding. Holding owner or editor, or getting the role through a group, doesn't count.

The check applies to the web UI, to `xmc login`, and to every gRPC request. See the [Configuration Reference](../INSTALL.md#configuration-reference) for how to set these values.

## Interactive Use: `xmc login`

Sign in as yourself with the `xmc` command, which [`install_xmanager.py`](../deploy/cli/README.md) installs into the XManager CLI virtual environment:

```bash
xmc login --server "${XMANAGER_UI_URL}"   # Or set XMC_API_URL / XMANAGER_UI_URL and run `xmc login`.
export XMC_AUTH_TOKEN=$(xmc print-token)
```

The XManager CLI and the [examples](../examples/) send `XMC_AUTH_TOKEN` as a Bearer token. Once it's set, you don't need `IAP_CLIENT_ID` or `XMC_CLIENT_SA`. Experiments you create are attributed to you.

- Tokens last 12 hours by default. When yours expires, run `xmc login` and export the token again.
- Run `xmc status` to see your account and expiry. `xmc logout` removes the cached token; server-side revocation is coming in a [google/xmc#1309](https://github.com/google/xmc/issues/1309) follow-up.
- Over SSH or on a headless machine, `xmc login` switches to a copy-and-paste flow on its own.

See the [`xmc` SDK README](../sdks/python/xmc/README.md#-authentication-xmc-login) for every subcommand, the credentials cache, and security notes.

> [!WARNING]
> Every child process inherits `XMC_AUTH_TOKEN`. Export it only in shells where you run XMC clients.

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
> Don't share one service account among people for interactive work. Use `xmc login` so each experiment is attributed to the person who ran it.

## Experiment Access (ACLs)

Each experiment has an ACL with three roles. Each role includes the ones below it:

| Role | Allows |
| :--- | :--- |
| `owners` | Everything a writer can do, plus deleting. |
| `writers` | Adding and changing work units, artifacts, and metadata. |
| `readers` | Viewing the experiment in the UI and through the API. |

A role can name a `user`, a `group`, a `service_account`, a `domain`, or `all_authenticated_users`.

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
