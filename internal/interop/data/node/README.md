# `node:*` pseudo-package scheme

Reserved for Node.js runtime APIs. No `.esc` files are populated yet.

The resolver recognizes the `node:` scheme but errors with
`node:* is reserved; not yet populated` for any import until Node support
lands. See `planning/builtins/implementation_plan.md` §2.2.
