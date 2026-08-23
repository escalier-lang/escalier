# §1 spike evidence: representative-method CFG dumps

These are the ESMeta control-flow-graph dumps the §1 findings are read from,
one text file per representative method. They let a reviewer check
[../spike_findings.md](../spike_findings.md) line by line without building
ESMeta.

Each file is the `logs/cfg/func/<name>.cfg` output of
`esmeta build-cfg -build-cfg:log`, copied verbatim. Regenerate them with
[../reproduce_spike.sh](../reproduce_spike.sh), which pins ESMeta
`7d237fd1`, one commit past the `v0.7.3` tag, and the ECMA-262 `es2025`
revision `84b38ad`. On a spec bump, diff the fresh dumps against these to
confirm the findings still hold.

| File | Shape it demonstrates |
| ---- | --------------------- |
| `INTRINSICS.Array.prototype.push.cfg` | direct receiver mutation + escape operand |
| `INTRINSICS.Array.prototype.fill.cfg` | receiver mutation returning receiver |
| `INTRINSICS.Array.prototype.sort.cfg` | receiver mutation returning receiver, via helper |
| `INTRINSICS.Array.prototype.slice.cfg` | fresh allocation, no receiver mutation |
| `INTRINSICS.Array.prototype.map.cfg` | fresh allocation + callback |
| `INTRINSICS.Array.prototype.forEach.cfg` | callback propagation (`Call` on a param) |
| `INTRINSICS.Map.prototype.set.cfg` | internal-slot mutation + escape into `[[MapData]]` |
| `INTRINSICS.Set.prototype.add.cfg` | internal-slot mutation + escape into `[[SetData]]` |
| `INTRINSICS.Object.freeze.cfg` | transitive mutation: delegates its whole write to `SetIntegrityLevel` |
| `SetIntegrityLevel.cfg` | the helper `Object.freeze` calls; writes the argument via seeded `DefinePropertyOrThrow` |
| `INTRINSICS.Reflect.set.cfg` | namespace function, param mutation + escape |
| `INTRINSICS.String.prototype.charAt.cfg` | immutable primitive, read-only receiver |
| `INTRINSICS.String.prototype.replace.cfg` | immutable primitive + callback |
| `INTRINSICS.String.prototype[%Symbol.iterator%].cfg` | symbol-keyed method name |
| `INTRINSICS.Number.prototype.toFixed.cfg` | domain `RangeError` throw + partial `yet` |
| `INTRINSICS.Promise.reject.cfg` | async reject via capability `[[Reject]]`, param origin |
| `INTRINSICS.Promise.all.cfg` | async reject, `IfAbruptRejectPromise`, combinator |
| `Set.cfg` | the `Set` abstract operation dispatching through dynamic `[[Set]]` |
