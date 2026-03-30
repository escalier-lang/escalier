import { type Dispatch, createContext, useContext, useReducer } from 'react';
import ReactDOM from 'react-dom/client';
import { FileChangeType, type FileEvent } from 'vscode-languageserver-protocol';

import wasmUrl from '../../bin/lsp-server.wasm?url';

import {
    type EditorAction,
    type EditorState,
    editorReducer,
    initialEditorState,
} from './editor-state';
import { BrowserFS } from './fs/browser-fs';
import { createVolume } from './fs/volume';
import { setupLanguage } from './language';
import { Client } from './lsp-client/client';
import { Playground } from './playground';
import {
    PlaygroundDispatchContext,
    PlaygroundStateContext,
    initialPlaygroundState,
    playgroundReducer,
} from './playground-state';

import './user-worker'; // sets up the monaco editor worker

const EditorStateContext = createContext<EditorState>(initialEditorState);
const EditorDispatchContext = createContext<Dispatch<EditorAction>>(() => {});

export function useEditorState(): EditorState {
    return useContext(EditorStateContext);
}

export function useEditorDispatch(): Dispatch<EditorAction> {
    return useContext(EditorDispatchContext);
}

const PlaygroundApp = ({ fs }: { fs: BrowserFS }) => {
    const [editorState, editorDispatch] = useReducer(
        editorReducer,
        initialEditorState,
    );
    const [playgroundState, playgroundDispatch] = useReducer(
        playgroundReducer,
        initialPlaygroundState,
    );

    return (
        <PlaygroundStateContext.Provider value={playgroundState}>
            <PlaygroundDispatchContext.Provider value={playgroundDispatch}>
                <EditorStateContext.Provider value={editorState}>
                    <EditorDispatchContext.Provider value={editorDispatch}>
                        <Playground
                            fs={fs}
                            editorState={editorState}
                            editorDispatch={editorDispatch}
                        />
                    </EditorDispatchContext.Provider>
                </EditorStateContext.Provider>
            </PlaygroundDispatchContext.Provider>
        </PlaygroundStateContext.Provider>
    );
};

async function main() {
    const wasmBuffer = await fetch(wasmUrl).then((res) => res.arrayBuffer());
    const baseUrl = import.meta.env.BASE_URL;

    const manifest: string[] = await fetch(
        `${baseUrl}types/manifest.json`,
    ).then((res) => res.json());

    const fs = new BrowserFS(createVolume(manifest, baseUrl));

    // Create a new client for the language server and
    // initialize it with the process ID and root URI.
    const client = new Client(wasmBuffer, '/', fs);
    client.run();
    const initResponse = await client.initialize({
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });
    console.log('initialize response', initResponse);

    // Forward filesystem change events to the LSP server.
    fs.events.on((event) => {
        let changes: FileEvent[];
        switch (event.type) {
            case 'rename':
                // LSP's didChangeWatchedFiles has no "rename" type — only Created,
                // Changed, and Deleted — so we translate a rename into a Deleted
                // event for the old path plus a Created event for the new path.
                changes = [
                    {
                        uri: `file://${event.oldPath}`,
                        type: FileChangeType.Deleted,
                    },
                    {
                        uri: `file://${event.path}`,
                        type: FileChangeType.Created,
                    },
                ];
                break;
            case 'create':
                changes = [
                    {
                        uri: `file://${event.path}`,
                        type: FileChangeType.Created,
                    },
                ];
                break;
            case 'change':
                changes = [
                    {
                        uri: `file://${event.path}`,
                        type: FileChangeType.Changed,
                    },
                ];
                break;
            case 'delete':
                changes = [
                    {
                        uri: `file://${event.path}`,
                        type: FileChangeType.Deleted,
                    },
                ];
                break;
        }
        client.workspaceDidChangeWatchedFiles({ changes });
    });

    setupLanguage(client, fs);

    const root = document.getElementById('root');

    if (!root) {
        throw new Error('Root element not found');
    }

    ReactDOM.createRoot(root).render(<PlaygroundApp fs={fs} />);
}

main().then(() => {
    console.log('App loaded');
});
