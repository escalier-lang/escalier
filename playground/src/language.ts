import * as monaco from 'monaco-editor-core';
import type * as lsp from 'vscode-languageserver-protocol';

import wasmUrl from '../../bin/lsp-server.wasm?url';

import { Client } from './lsp-client/client';
import { monarchLanguage } from './monarch-language';

export const languageID = 'escalier';

monaco.languages.register({ id: languageID });
monaco.languages.onLanguage(languageID, async () => {
    console.log(`onLanguage called for ${languageID}`);
    console.log('Client = ', Client);
    console.log('wasmUrl = ', wasmUrl);

    const wasmBuffer = await fetch(wasmUrl).then((res) => res.arrayBuffer());
    const client = new Client(wasmBuffer);

    client.run();
    client.on(
        'textDocument/publishDiagnostics',
        (params: lsp.PublishDiagnosticsParams) => {
            console.log('textDocument/publishDiagnostics', params);
        },
    );

    const initParams: lsp.InitializeParams = {
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    };
    const initResponse = await client.sendRequest('initialize', initParams);
    console.log('initialize response', initResponse);

    const didChangeParams: lsp.DidChangeTextDocumentParams = {
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
            version: 2,
        },
        contentChanges: [{ text: 'console.log("Hello, world!")\nval x =\n' }],
    };
    const didChangeResponse = await client.sendRequest(
        'textDocument/didChange',
        didChangeParams,
    );
    console.log('textDocument/didChange response', didChangeResponse);

    await client.stop();
});
monaco.languages.setMonarchTokensProvider(languageID, monarchLanguage);
monaco.languages.setLanguageConfiguration(languageID, {
    brackets: [
        ['(', ')'],
        ['{', '}'],
    ],
});
monaco.languages.registerHoverProvider(languageID, {
    provideHover(_model, _position, _token, _context) {
        return {
            contents: [
                { value: 'This should show the type of the hovered item' },
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
        monaco.languages.SemanticTokens | monaco.languages.SemanticTokensEdits
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
        { token: 'keyword', foreground: 'ce63eb' },
        { token: 'operator', foreground: 'FFFFFF' },
        { token: 'namespace', foreground: '66afce' },

        { token: 'string', foreground: 'DD9933' },
        { token: 'number', foreground: '00CC00' },
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
