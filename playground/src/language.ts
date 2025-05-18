import * as monaco from 'monaco-editor-core';
import * as lsp from 'vscode-languageserver-protocol';

import type { Client } from './lsp-client/client';
import { monarchLanguage } from './monarch-language';

export const languageID = 'escalier';

function convertSeverity(
    severity?: lsp.DiagnosticSeverity,
): monaco.MarkerSeverity {
    switch (severity) {
        case lsp.DiagnosticSeverity.Error:
            return monaco.MarkerSeverity.Error;
        case lsp.DiagnosticSeverity.Warning:
            return monaco.MarkerSeverity.Warning;
        case lsp.DiagnosticSeverity.Information:
            return monaco.MarkerSeverity.Info;
        case lsp.DiagnosticSeverity.Hint:
            return monaco.MarkerSeverity.Hint;
        default:
            return monaco.MarkerSeverity.Error;
    }
}

type Loc = {
    line: number;
    column: number;
};

type Span = {
    start: Loc;
    end: Loc;
};

type ErrorMesage = {
    message: string;
    span: Span;
};

export function setupLanguage(client: Client) {
    monaco.languages.register({ id: languageID });

    client.onTextDocumentPublishDiagnostics(
        (params: lsp.PublishDiagnosticsParams) => {
            const models = monaco.editor.getModels();

            if (params.uri.endsWith('.esc')) {
                client
                    .workspaceExecuteCommand({
                        command: 'compile',
                        arguments: [params.uri],
                    })
                    .catch((err) => {
                        if (err.message) {
                            try {
                                const errorMessages: Array<ErrorMesage> =
                                    JSON.parse(err.message);
                                for (const message of errorMessages) {
                                    const start = `${message.span.start.line}:${message.span.start.column}`;
                                    const end = `${message.span.end.line}:${message.span.end.column}`;
                                    console.log(
                                        `ERROR: ${start}-${end} ${message.message}`,
                                    );
                                }
                            } catch (e) {
                                console.error('Error message:', err.message);
                            }
                        }
                    })
                    .then((result) => {
                        if (!result) {
                            return;
                        }
                        const outputUri = params.uri.replace('.esc', '.js');
                        const model = models.find(
                            (model) => model.uri.toString() === outputUri,
                        );

                        if (!model) {
                            return;
                        }

                        model.setValue(result.text);
                    });
            }

            const model = models.find(
                (model) => model.uri.toString() === params.uri,
            );

            if (!model) {
                return;
            }

            const markers: monaco.editor.IMarkerData[] = params.diagnostics.map(
                (diagnostic): monaco.editor.IMarkerData => {
                    return {
                        severity: convertSeverity(diagnostic.severity),
                        startLineNumber: diagnostic.range.start.line + 1,
                        startColumn: diagnostic.range.start.character + 1,
                        endLineNumber: diagnostic.range.end.line + 1,
                        endColumn: diagnostic.range.end.character + 1,
                        message: diagnostic.message,
                    };
                },
            );
            monaco.editor.setModelMarkers(model, languageID, markers);
        },
    );

    monaco.editor.onDidCreateModel((model) => {
        console.log('onDidCreateModel', model.uri.toString());

        client.textDocumentDidOpen({
            textDocument: {
                uri: model.uri.toString(),
                languageId: model.getLanguageId(),
                version: model.getVersionId(),
                text: model.getValue(),
            },
        });

        model.onDidChangeContent(() => {
            client.textDocumentDidChange({
                textDocument: {
                    uri: model.uri.toString(),
                    version: model.getVersionId(),
                },
                contentChanges: [
                    {
                        text: model.getValue(),
                    },
                ],
            });
        });
    });

    monaco.editor.onWillDisposeModel((model) => {
        console.log('onWillDisposeModel', model.uri.toString());

        client.textDocumentDidClose({
            textDocument: {
                uri: model.uri.toString(),
            },
        });
    });

    monaco.languages.onLanguage(languageID, async () => {
        console.log(`onLanguage called for ${languageID}`);
    });

    monaco.languages.registerDeclarationProvider(languageID, {
        async provideDeclaration(model, position, _token) {
            try {
                const decl = await client.textDocumentDeclaration({
                    textDocument: {
                        uri: model.uri.toString(),
                    },
                    position: {
                        line: position.lineNumber,
                        character: position.column,
                    },
                });

                if (lsp.Location.is(decl)) {
                    return {
                        uri: monaco.Uri.parse(decl.uri),
                        range: {
                            startLineNumber: decl.range.start.line,
                            startColumn: decl.range.start.character,
                            endLineNumber: decl.range.end.line,
                            endColumn: decl.range.end.character,
                        },
                    };
                }

                throw new Error('TODO: handle Location[] and LocationLink[]');
            } catch (e) {
                console.error(e);
            }
        },
    });

    monaco.languages.registerDefinitionProvider(languageID, {
        async provideDefinition(model, position, _token) {
            try {
                console.log('provideDefinition called');
                const def = await client.textDocumentDefinition({
                    textDocument: {
                        uri: model.uri.toString(),
                    },
                    position: {
                        line: position.lineNumber,
                        character: position.column,
                    },
                });
                console.log('def = ', def);

                if (lsp.Location.is(def)) {
                    return {
                        uri: monaco.Uri.parse(def.uri),
                        range: {
                            startLineNumber: def.range.start.line,
                            startColumn: def.range.start.character,
                            endLineNumber: def.range.end.line,
                            endColumn: def.range.end.character,
                        },
                    };
                }

                throw new Error('TODO: handle Location[] and LocationLink[]');
            } catch (e) {
                console.error(e);
            }
        },
    });

    monaco.languages.setMonarchTokensProvider(languageID, monarchLanguage);
    monaco.languages.setLanguageConfiguration(languageID, {
        brackets: [
            ['(', ')'],
            ['{', '}'],
            ['[', ']'],
            ['<', '>'],
            ['`', '`'],
            ['"', '"'],
        ],
    });
    monaco.languages.registerHoverProvider(languageID, {
        async provideHover(model, position, _token, _context) {
            const hover = await client.textDocumentHover({
                textDocument: { uri: model.uri.toString() },
                position: {
                    line: position.lineNumber,
                    character: position.column,
                },
            });
            if (!hover) {
                return {
                    contents: [
                        {
                            value: 'TODO: return hover contents',
                        },
                    ],
                };
            }

            if (lsp.MarkupContent.is(hover.contents)) {
                return {
                    contents: [
                        {
                            value: hover.contents.value,
                        },
                    ],
                };
            }

            return {
                contents: [
                    {
                        value: 'TODO: hover - handle other types',
                    },
                ],
            };
        },
    });

    const legend: monaco.languages.SemanticTokensLegend = {
        tokenTypes: [
            'comment',
            'string',
            'keyword',
            'number',
            'regexp',
            'operator',
            'namespace',
            'type',
            'struct',
            'class',
            'interface',
            'enum',
            'typeParameter',
            'function',
            'member',
            'macro',
            'variable',
            'parameter',
            'property',
            'label',
        ],
        tokenModifiers: [
            'declaration',
            'documentation',
            'readonly',
            'static',
            'abstract',
            'deprecated',
            'modification',
            'async',
        ],
    };

    function getType(type: string) {
        return legend.tokenTypes.indexOf(type);
    }

    function getModifier(modifiers: string | string[]) {
        if (typeof modifiers === 'string') {
            // biome-ignore lint:
            modifiers = [modifiers];
        }
        if (Array.isArray(modifiers)) {
            let nModifiers = 0;
            for (const modifier of modifiers) {
                const nModifier = legend.tokenModifiers.indexOf(modifier);
                if (nModifier > -1) {
                    nModifiers |= (1 << nModifier) >>> 0;
                }
            }
            return nModifiers;
        }
        return 0;
    }

    const tokenPattern = /([a-zA-Z]+)((?:\.[a-zA-Z]+)*)/g;

    monaco.languages.registerDocumentSemanticTokensProvider(languageID, {
        getLegend() {
            return legend;
        },
        provideDocumentSemanticTokens(
            model,
            _lastResultId,
            _token,
        ): monaco.languages.ProviderResult<
            | monaco.languages.SemanticTokens
            | monaco.languages.SemanticTokensEdits
        > {
            const lines = model.getLinesContent();

            const data: number[] = [];

            let prevLine = 0;
            let prevChar = 0;

            for (let i = 0; i < lines.length; i++) {
                const line = lines[i];
                line.matchAll(tokenPattern);

                for (const match of line.matchAll(tokenPattern)) {
                    // translate token and modifiers to number representations
                    const type = getType(match[1]);
                    if (type === -1) {
                        continue;
                    }
                    const modifier = match[2].length
                        ? getModifier(match[2].split('.').slice(1))
                        : 0;

                    data.push(
                        // translate line to deltaLine
                        i - prevLine,
                        // for the same line, translate start to deltaStart
                        prevLine === i ? match.index - prevChar : match.index,
                        match[0].length,
                        type,
                        modifier,
                    );

                    prevLine = i;
                    prevChar = match.index;
                }
            }
            return {
                data: new Uint32Array(data),
                resultId: undefined,
            };
        },
        releaseDocumentSemanticTokens(_resultId: string | undefined) {},
    });

    monaco.editor.defineTheme('escalier-theme', {
        base: 'vs-dark',
        inherit: true,
        colors: {},
        rules: [
            { token: 'comment', foreground: 'aaaaaa', fontStyle: 'italic' },
            // { token: 'keyword', foreground: 'ce63eb' },
            { token: 'operator', foreground: 'FFFFFF' },
            { token: 'namespace', foreground: '66afce' },

            { token: 'string', foreground: 'DD9933' },
            // { token: 'number', foreground: '00CC00' },
            { token: 'type', foreground: '1db010' },
            { token: 'struct', foreground: '0000ff' },
            { token: 'class', foreground: '0000ff', fontStyle: 'italic bold' },
            { token: 'interface', foreground: '007700', fontStyle: 'bold' },
            { token: 'enum', foreground: '0077ff', fontStyle: 'bold' },
            { token: 'typeParameter', foreground: '1db010' },
            { token: 'function', foreground: '94763a' },

            { token: 'member', foreground: '94763a' },
            { token: 'macro', foreground: '615a60' },
            { token: 'variable', foreground: '3e5bbf' },
            { token: 'parameter', foreground: '3e5bbf' },
            { token: 'property', foreground: '3e5bbf' },
            { token: 'label', foreground: '615a60' },

            { token: 'type.static', fontStyle: 'bold' },
            { token: 'class.static', foreground: 'ff0000', fontStyle: 'bold' },
        ],
    });
}
