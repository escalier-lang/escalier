import * as monaco from 'monaco-editor-core';
import { useEffect, useRef } from 'react';

import styles from './editor.module.css';

import { initialCode } from './examples';
import { languageID } from './language';

export const Editor = () => {
    const divRef = useRef(null);

    useEffect(() => {
        const monacoEl = divRef.current;
        if (monacoEl) {
            const model = monaco.editor.createModel(
                initialCode,
                languageID,
                monaco.Uri.parse('file:///home/user/project/foo.esc'),
            );

            const editor = monaco.editor.create(monacoEl, {
                language: languageID,
                value: initialCode,
                theme: 'escalier-theme',
                bracketPairColorization: {
                    enabled: true,
                },
                model: model,
                fontSize: 14,
                automaticLayout: true,
                'semanticHighlighting.enabled': true,
            });

            return () => {
                model.dispose();
                editor.dispose();
            };
        }
    }, []);

    return <div className={styles.editor} ref={divRef} />;
};
