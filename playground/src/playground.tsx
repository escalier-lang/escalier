import * as monaco from 'monaco-editor-core';
import { useEffect, useRef, useState } from 'react';

import styles from './playground.module.css';

import { initialCode } from './examples';
import { languageID } from './language';

type OutputTab = 'js' | 'map';
type ActiveSide = 'input' | 'output';

export const Playground = () => {
    const inputDivRef = useRef(null);
    const outputDivRef = useRef(null);
    const outputEditorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(
        null,
    );
    const jsModelRef = useRef<monaco.editor.ITextModel | null>(null);
    const mapModelRef = useRef<monaco.editor.ITextModel | null>(null);
    const [activeOutputTab, setActiveOutputTab] = useState<OutputTab>('js');
    const [activeSide, setActiveSide] = useState<ActiveSide>('input');

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
                wordBasedSuggestions: 'off',
            });

            const jsModel = monaco.editor.createModel(
                '',
                'javascript',
                monaco.Uri.parse('file:///home/user/project/foo.js'),
            );
            jsModelRef.current = jsModel;

            const mapModel = monaco.editor.createModel(
                '',
                'json',
                monaco.Uri.parse('file:///home/user/project/foo.js.map'),
            );
            mapModelRef.current = mapModel;

            const outputEditor = monaco.editor.create(outputElem, {
                theme: 'escalier-theme',
                bracketPairColorization: {
                    enabled: true,
                },
                model: jsModel,
                fontSize: 14,
                automaticLayout: true,
                readOnly: true,
            });
            outputEditorRef.current = outputEditor;

            const inputFocusDisposable = inputEditor.onDidFocusEditorWidget(
                () => setActiveSide('input'),
            );
            const outputFocusDisposable = outputEditor.onDidFocusEditorWidget(
                () => setActiveSide('output'),
            );

            return () => {
                inputFocusDisposable.dispose();
                outputFocusDisposable.dispose();
                inputModel.dispose();
                inputEditor.dispose();
                jsModel.dispose();
                mapModel.dispose();
                outputEditor.dispose();
                jsModelRef.current = null;
                mapModelRef.current = null;
                outputEditorRef.current = null;
            };
        }
    }, []);

    const switchOutputTab = (tab: OutputTab) => {
        setActiveOutputTab(tab);
        setActiveSide('output');
        const model = tab === 'js' ? jsModelRef.current : mapModelRef.current;
        if (outputEditorRef.current && model) {
            outputEditorRef.current.setModel(model);
        }
    };

    const tabClass = (isActive: boolean, isVisible: boolean) =>
        `${styles.tab} ${isActive ? styles.activeTab : isVisible ? styles.visibleTab : ''}`;

    return (
        <div className={styles.playground}>
            <div className={styles.header} role="tablist">
                <div
                    role="tab"
                    tabIndex={0}
                    aria-selected={activeSide === 'input'}
                    aria-controls="input-panel"
                    className={tabClass(activeSide === 'input', true)}
                    onClick={() => setActiveSide('input')}
                    onKeyDown={(e) => {
                        if (e.key === 'Enter' || e.key === ' ')
                            setActiveSide('input');
                    }}
                >
                    foo.esc
                </div>
            </div>
            <div className={styles.header} role="tablist">
                <div
                    role="tab"
                    tabIndex={0}
                    aria-selected={
                        activeSide === 'output' && activeOutputTab === 'js'
                    }
                    aria-controls="output-panel"
                    className={tabClass(
                        activeSide === 'output' && activeOutputTab === 'js',
                        activeOutputTab === 'js',
                    )}
                    onClick={() => switchOutputTab('js')}
                    onKeyDown={(e) => {
                        if (e.key === 'Enter' || e.key === ' ')
                            switchOutputTab('js');
                    }}
                >
                    foo.js
                </div>
                <div
                    role="tab"
                    tabIndex={0}
                    aria-selected={
                        activeSide === 'output' && activeOutputTab === 'map'
                    }
                    aria-controls="output-panel"
                    className={tabClass(
                        activeSide === 'output' && activeOutputTab === 'map',
                        activeOutputTab === 'map',
                    )}
                    onClick={() => switchOutputTab('map')}
                    onKeyDown={(e) => {
                        if (e.key === 'Enter' || e.key === ' ')
                            switchOutputTab('map');
                    }}
                >
                    foo.js.map
                </div>
            </div>
            <div id="input-panel" role="tabpanel" className={styles.editor} ref={inputDivRef} />
            <div id="output-panel" role="tabpanel" className={styles.editor} ref={outputDivRef} />
        </div>
    );
};
