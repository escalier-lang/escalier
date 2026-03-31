import { create } from 'zustand';

import {
    type PlaygroundAction,
    type PlaygroundState,
    initialPlaygroundState,
    playgroundReducer,
} from './playground-state';

type PlaygroundStore = PlaygroundState & {
    dispatch: (action: PlaygroundAction) => void;
};

export const usePlaygroundStore = create<PlaygroundStore>((set) => ({
    ...initialPlaygroundState,
    dispatch: (action) => set((state) => playgroundReducer(state, action)),
}));
