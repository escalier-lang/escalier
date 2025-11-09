import editorWorker from 'monaco-editor-core/esm/vs/editor/editor.worker?worker';

// The editor worker is what runs the non-language specific parts of the editor.
// We need to provide monaco-editor an instance of it when it asks for it.
(self as any).MonacoEnvironment = {
    getWorker(_moduleId: string, _label: string) {
        return new editorWorker();
    },
};
