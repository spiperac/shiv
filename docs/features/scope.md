# Scope

Scope controls which hosts Shiv actively intercepts and records. Traffic from out-of-scope hosts passes through the proxy transparently without being stored.

## Adding hosts to scope

Go to the **Scope** tab and add a hostname. Entries are matched against the `Host` header of each request. Matching is by hostname only — port and path are not considered.

You can add as many hosts as needed. Wildcard entries are not currently supported; each entry is an exact hostname match.

## Effect on other tabs

When a scope is defined, the **History** tab gains an **In scope only** toggle in the filter bar that limits the view to scoped traffic. Out-of-scope requests are still captured if the setting to record all traffic is on, but the filter lets you focus on what matters.

The **Intercept** tab only pauses requests from in-scope hosts. This prevents the intercept queue from filling up with CDN and analytics traffic when you only care about a specific target.

## No scope

If the scope list is empty, Shiv treats all hosts as in scope — everything is intercepted and recorded. This is the default on a fresh project.
