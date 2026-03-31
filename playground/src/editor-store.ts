import { create } from 'zustand';

import {
    type EditorAction,
    type EditorState,
    editorReducer,
    initialEditorState,
} from './editor-state';

type EditorStore = EditorState & {
    dispatch: (action: EditorAction) => void;
};

export const useEditorStore = create<EditorStore>((set) => ({
    ...initialEditorState,
    dispatch: (action) => set((state) => editorReducer(state, action)),
}));
