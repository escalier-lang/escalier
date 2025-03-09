import * as monaco from 'monaco-editor';
import editorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker';

// The editor worker is what runs the non-language specific parts of the editor.
// We need to provide monaco-editor an instance of it when it asks for it.
self.MonacoEnvironment = {
    getWorker(_moduleId: string, _label: string) {
        return new editorWorker();
    },
};

monaco.languages.typescript.typescriptDefaults.setEagerModelSync(true);
