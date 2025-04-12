import * as monaco from 'monaco-editor-core';
import { useEffect, useRef } from 'react';

import styles from './playground.module.css';

import { initialCode } from './examples';
import { languageID } from './language';

export const Playground = () => {
    const inputDivRef = useRef(null);
    const outputDivRef = useRef(null);

    useEffect(() => {
        const inputElem = inputDivRef.current;
        const outputElem = outputDivRef.current;

        if (inputElem && outputElem) {
            const inputModel = monaco.editor.createModel(
                initialCode,
                languageID,
                monaco.Uri.parse('file:///home/user/project/foo.esc'),
            );

            const inputEditor = monaco.editor.create(inputElem, {
                theme: 'escalier-theme',
                bracketPairColorization: {
                    enabled: true,
                },
                model: inputModel,
                fontSize: 14,
                automaticLayout: true,
                'semanticHighlighting.enabled': true,
            });

            const outputModel = monaco.editor.createModel(
                '',
                'javascript',
                monaco.Uri.parse('file:///home/user/project/foo.js'),
            );

            const outputEditor = monaco.editor.create(outputElem, {
                theme: 'escalier-theme',
                bracketPairColorization: {
                    enabled: true,
                },
                model: outputModel,
                fontSize: 14,
                automaticLayout: true,
                readOnly: true,
            });

            return () => {
                inputModel.dispose();
                inputEditor.dispose();
                outputModel.dispose();
                outputEditor.dispose();
            };
        }
    }, []);

    return (
        <div className={styles.playground}>
            <div>Input</div>
            <div>Output</div>
            <div className={styles.editor} ref={inputDivRef} />
            <div className={styles.editor} ref={outputDivRef} />
        </div>
    );
};
