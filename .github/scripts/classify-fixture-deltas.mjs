#!/usr/bin/env node
//
// Classifies how a PR changes the committed fixture outputs and rates each
// change by review risk.
//
//     classify-fixture-deltas.mjs <base-ref> [--json <out.json>] [--comment <out.md>]
//
// The fixture harness in cmd/escalier/fixture_test.go fails unless each
// fixture's committed build/ tree and error.txt match what the compiler
// produces. A change to those files is therefore a record of how the
// compiler's behavior changed on that fixture's source. The script diffs
// them between the merge base of <base-ref> and HEAD and assigns one of
// three tiers to each change:
//
//   - high: a diagnostic disappears while the fixture's source stays the
//     same. The compiler now accepts code it rejected, which is where a
//     soundness hole shows up.
//   - medium: a diagnostic that disappears after an edit to the fixture's
//     source, a new diagnostic on unchanged source, a change to inferred
//     types or emitted JS on unchanged source, or a removed or disabled
//     fixture.
//   - low: a reworded diagnostic, a source-map-only change, a new fixture,
//     or any other output change that follows an edit to the fixture's
//     own source.
//
// The script prints a Markdown report, writes it to $GITHUB_STEP_SUMMARY
// when that is set, and emits a workflow annotation for each high and
// medium change. With --comment it also writes the report as the body of
// a PR comment that starts with COMMENT_MARKER. It exits 0 whatever tiers it finds, since the tiers are
// advisory and do not gate the merge.

import { execFileSync } from 'node:child_process';
import { appendFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

export const TIERS = ['low', 'medium', 'high'];

const DIAGNOSTIC_HEADER = /^(\S+?):(\d+):(\d+): (.*)$/;

// Returns one entry per diagnostic in an error.txt, in file order. Each
// entry has the fixture-relative `file`, the 1-based `line` and `column`,
// and the `message`.
export function parseDiagnostics(text) {
    const diagnostics = [];
    for (const raw of text.split('\n')) {
        const match = DIAGNOSTIC_HEADER.exec(raw);
        if (match) {
            diagnostics.push({
                file: match[1],
                line: Number(match[2]),
                column: Number(match[3]),
                message: match[4],
            });
        }
    }
    return diagnostics;
}

// Returns the diagnostics that only `before` has, the ones that only
// `after` has, and the pairs that sit at the same position with different
// messages. Diagnostics match on file and message, so an edit that shifts a
// diagnostic to another line does not count as a removal plus an addition.
export function diffDiagnostics(before, after) {
    const key = (d) => `${d.file}\0${d.message}`;
    const remaining = new Map();
    for (const d of after) {
        const list = remaining.get(key(d)) ?? [];
        list.push(d);
        remaining.set(key(d), list);
    }

    let removed = [];
    for (const d of before) {
        const list = remaining.get(key(d));
        if (list && list.length > 0) {
            list.shift();
        } else {
            removed.push(d);
        }
    }
    let added = [...remaining.values()].flat();

    const position = (d) => `${d.file}:${d.line}:${d.column}`;
    const reworded = [];
    for (const old of [...removed]) {
        const match = added.find((d) => position(d) === position(old));
        if (match) {
            reworded.push({ before: old, after: match });
            removed = removed.filter((d) => d !== old);
            added = added.filter((d) => d !== match);
        }
    }

    return { removed, added, reworded };
}

// Returns the lines that only `before` has and the lines that only `after`
// has, ignoring order and blank lines. Each .d.ts output declares one
// binding per line, so the result lists the declarations whose types
// changed.
export function diffLines(before, after) {
    const count = (text) => {
        const counts = new Map();
        for (const line of text.split('\n')) {
            if (line.trim() !== '') {
                counts.set(line, (counts.get(line) ?? 0) + 1);
            }
        }
        return counts;
    };
    const subtract = (a, b) => {
        const out = [];
        for (const [line, n] of a) {
            for (let i = 0; i < n - (b.get(line) ?? 0); i++) {
                out.push(line);
            }
        }
        return out;
    };
    const a = count(before);
    const b = count(after);
    return { removed: subtract(a, b), added: subtract(b, a) };
}

// Classifies the changes to one fixture. `files` maps each changed path,
// relative to the fixture directory, to `{ before, after }`. Either side is
// `null` when the file does not exist at that revision. Returns a list of
// findings, each with a `tier`, a `kind`, a one-line `summary`, an optional
// `file` and `line` to annotate, and `details` lines for the report.
export function classifyFixture(files) {
    const paths = Object.keys(files);
    const isOutput = (p) =>
        p === 'error.txt' || p.startsWith('build/') || p === 'DISABLED';
    const sourceChanged = paths.some((p) => !isOutput(p));
    const findings = [];

    // Every fixture carries a package.json, so its creation or deletion marks
    // the fixture itself as added or removed.
    const manifest = files['package.json'];
    if (manifest && manifest.before === null) {
        return [
            {
                tier: 'low',
                kind: 'fixture-added',
                summary: 'New fixture',
                details: [],
            },
        ];
    }
    if (manifest && manifest.after === null) {
        return [
            {
                tier: 'medium',
                kind: 'fixture-removed',
                summary: 'Fixture removed, so its coverage is gone',
                details: [],
            },
        ];
    }

    const disabled = files.DISABLED;
    if (disabled && disabled.before === null) {
        findings.push({
            tier: 'medium',
            kind: 'fixture-disabled',
            summary: `Fixture disabled: ${disabled.after.trim()}`,
            file: 'DISABLED',
            details: [],
        });
    }

    // An output change that follows an edit to the fixture's own source
    // reflects the new test input, not a change in the compiler.
    const onUnchanged = (tier) => (sourceChanged ? 'low' : tier);
    const sourceNote = sourceChanged ? ' after its source changed' : '';

    const errors = files['error.txt'];
    if (errors) {
        const { removed, added, reworded } = diffDiagnostics(
            parseDiagnostics(errors.before ?? ''),
            parseDiagnostics(errors.after ?? ''),
        );
        for (const d of removed) {
            findings.push({
                tier: sourceChanged ? 'medium' : 'high',
                kind: 'diagnostic-removed',
                summary: `Compiler no longer reports \`${d.message}\`${sourceNote}`,
                file: d.file,
                line: d.line,
                details: [`${d.file}:${d.line}:${d.column}: ${d.message}`],
            });
        }
        for (const d of added) {
            findings.push({
                tier: onUnchanged('medium'),
                kind: 'diagnostic-added',
                summary: `Compiler now reports \`${d.message}\`${sourceNote}`,
                file: d.file,
                line: d.line,
                details: [`${d.file}:${d.line}:${d.column}: ${d.message}`],
            });
        }
        for (const { before, after } of reworded) {
            findings.push({
                tier: 'low',
                kind: 'diagnostic-reworded',
                summary: 'Diagnostic message changed',
                file: after.file,
                line: after.line,
                details: [`- ${before.message}`, `+ ${after.message}`],
            });
        }
    }

    for (const path of paths.filter((p) => p.startsWith('build/'))) {
        const { before, after } = files[path];
        if (path.endsWith('.d.ts')) {
            const { removed, added } = diffLines(before ?? '', after ?? '');
            findings.push({
                tier: onUnchanged('medium'),
                kind: 'types-changed',
                summary: `Inferred types changed in \`${path}\`${sourceNote}`,
                file: path,
                details: [
                    ...removed.map((l) => `- ${l}`),
                    ...added.map((l) => `+ ${l}`),
                ],
            });
        } else if (path.endsWith('.js')) {
            findings.push({
                tier: onUnchanged('medium'),
                kind: 'js-changed',
                summary: `Emitted JS changed in \`${path}\`${sourceNote}`,
                file: path,
                details: [],
            });
        } else if (path.endsWith('.js.map')) {
            findings.push({
                tier: 'low',
                kind: 'sourcemap-changed',
                summary: `Source map changed in \`${path}\``,
                file: path,
                details: [],
            });
        }
    }

    return findings;
}

function git(args, stderr = 'inherit') {
    return execFileSync('git', args, {
        encoding: 'utf8',
        maxBuffer: 64 * 1024 * 1024,
        stdio: ['ignore', 'pipe', stderr],
    });
}

// Returns the contents of `path` at `rev`, or `null` when the path does
// not exist there.
function readAt(rev, path) {
    try {
        // A path missing at one side of the diff is an expected answer, so
        // git's complaint about it stays out of the job log.
        return git(['show', `${rev}:${path}`], 'ignore');
    } catch {
        return null;
    }
}

// Returns the changed fixture files between `base` and HEAD, grouped by
// fixture name, in the shape classifyFixture reads.
function collectChanges(base) {
    const changed = git([
        'diff',
        '--name-only',
        '--no-renames',
        '-z',
        base,
        'HEAD',
        '--',
        'fixtures',
    ])
        .split('\0')
        .filter(Boolean);

    const byFixture = new Map();
    for (const path of changed) {
        const [, name, ...rest] = path.split('/');
        if (rest.length === 0) {
            continue;
        }
        const files = byFixture.get(name) ?? {};
        files[rest.join('/')] = {
            before: readAt(base, path),
            after: readAt('HEAD', path),
        };
        byFixture.set(name, files);
    }
    return byFixture;
}

function escapeAnnotation(text) {
    return text
        .replaceAll('%', '%25')
        .replaceAll('\r', '%0D')
        .replaceAll('\n', '%0A');
}

function annotate(fixture, finding) {
    const level = finding.tier === 'high' ? 'warning' : 'notice';
    const props = [`title=Fixture risk: ${finding.tier}`];
    if (finding.file) {
        props.push(`file=fixtures/${fixture}/${finding.file}`);
    }
    if (finding.line) {
        props.push(`line=${finding.line}`);
    }
    console.log(
        `::${level} ${props.join(',')}::${escapeAnnotation(finding.summary)}`,
    );
}

// Renders the findings as a Markdown report with the riskiest fixtures
// first.
export function renderReport(results) {
    if (results.length === 0) {
        return '## Fixture deltas\n\nNo fixture outputs changed.\n';
    }
    const rank = (r) => TIERS.indexOf(r.tier);
    const sorted = [...results].sort(
        (a, b) => rank(b) - rank(a) || a.fixture.localeCompare(b.fixture),
    );
    const counts = TIERS.map(
        (t) => `${results.filter((r) => r.tier === t).length} ${t}`,
    ).reverse();

    const lines = [
        '## Fixture deltas',
        '',
        `${results.length} fixtures changed: ${counts.join(', ')}.`,
        '',
    ];
    for (const r of sorted) {
        lines.push(`### \`${r.fixture}\`: ${r.tier}`, '');
        for (const f of r.findings) {
            lines.push(`- **${f.tier}**: ${f.summary}`);
        }
        const details = r.findings.flatMap((f) => f.details);
        if (details.length > 0) {
            lines.push('', '```diff', ...details, '```');
        }
        lines.push('');
    }
    return lines.join('\n');
}

// The first line of the PR comment. The workflow finds the comment it
// posted on an earlier run by this line and edits it in place.
export const COMMENT_MARKER = '<!-- classify-fixture-deltas -->';

// GitHub rejects a comment body longer than 65,536 characters.
const COMMENT_LIMIT = 60000;

// Returns the body of the PR comment for `report`. A report that does not
// fit in a comment is cut at a line boundary and ends with a pointer to
// the job summary, which holds the full report.
export function renderComment(report) {
    let body = report;
    if (body.length > COMMENT_LIMIT) {
        const cut = body.lastIndexOf('\n', COMMENT_LIMIT);
        body = body.slice(0, cut);
        // A cut inside a diff block leaves the fence open, so close it.
        if ((body.match(/^```/gm) ?? []).length % 2 === 1) {
            body += '\n```';
        }
        body +=
            '\n\nThe report is too long for a comment. The job summary of ' +
            'the `classify_fixture_deltas` check has the rest.\n';
    }
    return `${COMMENT_MARKER}\n${body}`;
}

function main(argv) {
    const [baseRef, ...rest] = argv;
    if (!baseRef) {
        console.error(
            'usage: classify-fixture-deltas.mjs <base-ref> [--json <out.json>] [--comment <out.md>]',
        );
        process.exit(2);
    }
    const jsonIndex = rest.indexOf('--json');
    const jsonPath = jsonIndex >= 0 ? rest[jsonIndex + 1] : null;
    const commentIndex = rest.indexOf('--comment');
    const commentPath = commentIndex >= 0 ? rest[commentIndex + 1] : null;

    const base = git(['merge-base', baseRef, 'HEAD']).trim();
    const results = [];
    for (const [fixture, files] of collectChanges(base)) {
        const findings = classifyFixture(files);
        if (findings.length === 0) {
            continue;
        }
        const tier =
            TIERS[Math.max(...findings.map((f) => TIERS.indexOf(f.tier)))];
        results.push({ fixture, tier, findings });
        for (const f of findings.filter((f) => f.tier !== 'low')) {
            annotate(fixture, f);
        }
    }

    const report = renderReport(results);
    console.log(report);
    if (process.env.GITHUB_STEP_SUMMARY) {
        appendFileSync(process.env.GITHUB_STEP_SUMMARY, report);
    }
    if (jsonPath) {
        writeFileSync(
            jsonPath,
            `${JSON.stringify({ base, results }, null, 2)}\n`,
        );
    }
    if (commentPath) {
        writeFileSync(commentPath, renderComment(report));
    }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    main(process.argv.slice(2));
}
