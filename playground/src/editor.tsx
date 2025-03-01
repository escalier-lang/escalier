import { useRef, useState, useEffect } from 'react';
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api';

import styles from './editor.module.css';

// const languageId = 'escalier';
// monaco.languages.register({ id: languageId });
// monaco.languages.onLanguage(languageId, async () => {
//     mode = (await getMode()).setupMode(defaults);
// });

// monaco.languages.onLanguage(languageID, () => {
//     monaco.languages.setMonarchTokensProvider(languageID, monarchLanguage);
//     monaco.languages.setLanguageConfiguration(languageID, richLanguageConfiguration);
//     const client = new WorkerManager();

//     const worker: WorkerAccessor = (...uris: monaco.Uri[]): Promise<TodoLangWorker> => {
//         return client.getLanguageServiceWorker(...uris);
//     };
//     //Call the errors provider
//     new DiagnosticsAdapter(worker);
//     monaco.languages.registerDocumentFormattingEditProvider(languageID, new TodoLangFormattingProvider(worker));
// });

export const Editor = () => {
	const [editor, setEditor] = useState<monaco.editor.IStandaloneCodeEditor | null>(null);
	const monacoEl = useRef(null);

	useEffect(() => {
		if (monacoEl) {
			setEditor((editor) => {
				if (editor) return editor;

				const newEditor = monaco.editor.create(monacoEl.current!, {
					value: ['<html>', '<body>', '</body>', '</html>'].join('\n'),
					language: 'html',
				});

                monaco.editor.setTheme('vs-dark');

                return newEditor;
			});
		}

		return () => editor?.dispose();
	}, [monacoEl.current]);

	return <div className={styles.editor} ref={monacoEl}></div>;
};
