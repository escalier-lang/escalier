import { useRef, useState, useEffect } from 'react';
import * as monaco from 'monaco-editor-core';

import {monarchLanguage} from './monarch-language';
import styles from './editor.module.css';

const languageID = 'escalier';

monaco.languages.register({ id: languageID })
monaco.languages.setMonarchTokensProvider(languageID, monarchLanguage)
monaco.languages.registerHoverProvider(languageID, {
	provideHover(_model, _position, _token, _context) {
		return {
			contents: [{ value: 'This should show the type of the hovered item' }],
		};
	}
});

const initialCode =  `// Type source code in your language here...
class MyClass {
  @attribute
  void main() {
    Console.writeln( "Hello Monarch world");
  }
}`;

export const Editor = () => {
	const [editor, setEditor] = useState<monaco.editor.IStandaloneCodeEditor | null>(null);
	const monacoEl = useRef(null);

	useEffect(() => {
		if (monacoEl) {
			setEditor((editor) => {
				if (editor) return editor;

				const newEditor = monaco.editor.create(monacoEl.current!, {
					language: languageID,
					value: initialCode,
					theme: 'vs-dark',
				});

				const model = newEditor.getModel();
				model?.onDidChangeContent(() => {
					console.log(model?.getValue());

					const markers = [{
						severity: monaco.MarkerSeverity.Error,
						startLineNumber: 2,
						startColumn: 7,
						endLineNumber: 2,
						endColumn: 14,
						message: 'this is an error'
					}];

					monaco.editor.setModelMarkers(model, languageID, markers);
				});

                return newEditor;
			});
		}

		return () => editor?.dispose();
	}, [monacoEl.current]);

	return <div className={styles.editor} ref={monacoEl}></div>;
};
