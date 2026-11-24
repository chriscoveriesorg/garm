# Scale-set preference

GARM reads the optional `scale-set-preferences.json` beside its configured
`config.toml` at startup. Cifast owns the file. Override its path with
`default.scale_set_preference_file` when needed. Missing, invalid, or mismatched
configuration leaves normal capacity in place.

For the first window, configure these two rows.

```json
{"scale_sets":[
  {"id":26,"name":"webclip-org","priority":1},
  {"id":33,"name":"ci7550-shared","priority":3}
]}
```

To widen the preference after the canary, add
`{"id":25,"name":"claw-org","priority":2}`. MacDad uses separate labels.
Priority applies only within a forge entity and runner group, to enabled
sets with identical nonempty labels. Both the database ID and name must match.

GitHub receives an absolute maximum. When a preferred listener has room,
the overflow listener advertises its own broker-reported assigned count.
A zero header freezes a known-idle overflow listener. Missing statistics for
either side, a missing preferred match, or full preferred sets restore the
configured maximum. A reduced request that fails retries at the normal
maximum using the same session. No database migration or provider field changes
are required.

At the deployed 158fe569 tree, the listener calls GetMessage from
`workers/scaleset/scaleset_listener.go` at lines 286–287, and
`util/github/scalesets/message_sessions.go` sets the capacity header. The
preference watches session statistics and ends only a pending request whose
advertisement needs to change. The message cursor and session are retained.

The two-job canary should show the first assignment on Webclip and overflow
when Webclip is full. Check that the zero-header listener keeps its session,
then accepts an assignment after its advertisement is restored. The source
and tests prove the value GARM sends. Actual assignment is observed in the
controller journal during the seat's window.

Build the patched 158fe569 source through the existing CIFAST upstream update
workflow. Its prerelease contains `garm`, `garm-cli`, `SHA256SUMS`, and the
manifest binding the patch and applied tree.
