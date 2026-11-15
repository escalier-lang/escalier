import type * as monaco from 'monaco-editor-core';
import type * as lsp from 'vscode-languageserver-protocol';

// Monaco is imported for its types alone, so this module loads no Monaco code and
// its tests run without a browser. The provider receives the model's Uri from
// Monaco and hands it back as each edit's resource.

export type CodeActionRequest = (
    params: lsp.CodeActionParams,
) => Promise<(lsp.Command | lsp.CodeAction)[] | null>;

export type CodeActionProviderOptions = {
    /** Sends `textDocument/codeAction` to the server. */
    request: CodeActionRequest;
    /** Sends any change to the document at uri that has not been sent yet. */
    flushPendingChanges: (uri: string) => void;
    /** Returns the diagnostics the server last published for the document at uri. */
    publishedDiagnostics: (uri: string) => lsp.Diagnostic[];
};

// Monaco counts lines and columns from 1, and LSP from 0.

function toLspRange(range: monaco.IRange): lsp.Range {
    return {
        start: {
            line: range.startLineNumber - 1,
            character: range.startColumn - 1,
        },
        end: { line: range.endLineNumber - 1, character: range.endColumn - 1 },
    };
}

function toMonacoRange(range: lsp.Range): monaco.IRange {
    return {
        startLineNumber: range.start.line + 1,
        startColumn: range.start.character + 1,
        endLineNumber: range.end.line + 1,
        endColumn: range.end.character + 1,
    };
}

function sameRange(a: lsp.Range, b: lsp.Range): boolean {
    return (
        a.start.line === b.start.line &&
        a.start.character === b.start.character &&
        a.end.line === b.end.line &&
        a.end.character === b.end.character
    );
}

// markerDiagnostics returns the LSP diagnostic each of markers was made from. A
// marker carries no `data`, which a quick fix such as the borrow fix reads, so
// each marker is matched by range and message to a diagnostic the server
// published. A marker no published diagnostic matches is rebuilt from its own
// range and message.
export function markerDiagnostics(
    markers: monaco.editor.IMarkerData[],
    published: lsp.Diagnostic[],
): lsp.Diagnostic[] {
    return markers.map((marker) => {
        const range = toLspRange(marker);
        return (
            published.find(
                (d) =>
                    d.message === marker.message && sameRange(d.range, range),
            ) ?? { range, message: marker.message }
        );
    });
}

// documentTextEdits returns the text edits edit makes to the document at uri, from
// its `changes` map and from each of its `documentChanges` entries that edits
// that document. A file create, rename or delete in `documentChanges` is left out.
export function documentTextEdits(
    edit: lsp.WorkspaceEdit,
    uri: string,
): lsp.TextEdit[] {
    const edits = [...(edit.changes?.[uri] ?? [])];
    for (const change of edit.documentChanges ?? []) {
        if ('textDocument' in change && change.textDocument.uri === uri) {
            for (const e of change.edits) {
                if ('newText' in e) {
                    edits.push(e);
                }
            }
        }
    }
    return edits;
}

// convertCodeActions returns the Monaco form of each action in the server's
// response that carries an edit to the document at uri. A bare command and an
// action with only a command are left out, since the playground registers no
// command for Monaco to run. resource is the document's Monaco Uri, and
// versionId the model version the edits are offsets into, so Monaco declines
// to apply them once the document has changed. markers are the markers the
// request was made for, and an action names the ones its diagnostics match.
export function convertCodeActions(
    actions: (lsp.Command | lsp.CodeAction)[] | null,
    uri: string,
    resource: monaco.Uri,
    versionId: number,
    markers: monaco.editor.IMarkerData[],
): monaco.languages.CodeAction[] {
    const result: monaco.languages.CodeAction[] = [];
    for (const action of actions ?? []) {
        if (!('edit' in action) || !action.edit) {
            continue;
        }
        const textEdits = documentTextEdits(action.edit, uri);
        if (textEdits.length === 0) {
            continue;
        }
        const diagnostics = (action.diagnostics ?? []).flatMap((d) =>
            markers.filter(
                (m) =>
                    m.message === d.message &&
                    sameRange(toLspRange(m), d.range),
            ),
        );
        result.push({
            title: action.title,
            kind: action.kind,
            isPreferred: action.isPreferred,
            diagnostics: diagnostics.length > 0 ? diagnostics : undefined,
            edit: {
                edits: textEdits.map((edit) => ({
                    resource,
                    versionId,
                    textEdit: {
                        range: toMonacoRange(edit.range),
                        text: edit.newText,
                    },
                })),
            },
        });
    }
    return result;
}

/**
 * Returns a Monaco provider of the server's code actions for a range, such as the
 * quick fix that writes a missing borrow and the refactor that adds the arms a
 * `match` is missing. It returns no actions when the request fails.
 */
export function createCodeActionProvider({
    request,
    flushPendingChanges,
    publishedDiagnostics,
}: CodeActionProviderOptions): monaco.languages.CodeActionProvider {
    return {
        async provideCodeActions(model, range, context, _token) {
            const uri = model.uri.toString();
            try {
                // The server offers no edit while its last check is older than
                // the document, so a pending change goes out first.
                flushPendingChanges(uri);
                const versionId = model.getVersionId();
                const actions = await request({
                    textDocument: { uri },
                    range: toLspRange(range),
                    context: {
                        diagnostics: markerDiagnostics(
                            context.markers,
                            publishedDiagnostics(uri),
                        ),
                        only: context.only ? [context.only] : undefined,
                    },
                });
                return {
                    actions: convertCodeActions(
                        actions,
                        uri,
                        model.uri,
                        versionId,
                        context.markers,
                    ),
                    dispose() {},
                };
            } catch (e) {
                console.error('Code action error:', e);
                return { actions: [], dispose() {} };
            }
        },
    };
}
