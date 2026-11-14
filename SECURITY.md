# Security policy

## Scope

TreeSitterMCP analyzes operator-authorized local projects. It does not execute
project code. Allow roots are operator policy; MCP cannot widen them. Rooted file
operations, symlink rejection for reads, metadata-only directory scans, explicit
credential/dependency exclusions, input validation, and resource limits reduce
exposure. They do not prove that every source file is secret-free or sandbox a
bug in native parser code.

Run as an ordinary user with access only to intended projects. Keep state outside
those projects, use a separate state directory per process, and protect parser
libraries as trusted executable code. Do not replace libraries while running.
Treat source, filenames, and tool results as untrusted data rather than
instructions to an assistant.

## Reporting a vulnerability

Please **do not** open a public issue for security problems. Use GitHub's private
vulnerability reporting instead: open the repository's **Security** tab and choose
**Report a vulnerability**.

Do not include credentials, private code, or sensitive paths in any report.

Include affected versions, OS/architecture, required access, a minimal synthetic
reproduction, impact, and any workaround. Maintenance response times are not
promised by this source distribution.

## Verification boundaries

Current tests cover path traversal and link escapes, excluded paths, bounded
input/output, metadata scan limits, cancellation, pagination invalidation, cache
ownership, and interoperability. Parser execution is native and cooperative;
limits are not a hard process-level memory/time sandbox. The local report covers
Linux x86-64; it does not assert a hosted security audit or untested platforms.
