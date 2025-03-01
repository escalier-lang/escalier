// @ts-expect-error: there are no types for this module
import * as worker from 'monaco-editor-core/esm/vs/editor/editor.worker';

import { EscalierWorker } from './escalier-worker';
  
self.onmessage = () => {
	// ignore the first message
    // @ts-expect-error: worker.initialize is untyped
	worker.initialize((ctx, createData) => {
		return new EscalierWorker(ctx, createData);
	});
};