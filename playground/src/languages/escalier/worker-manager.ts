/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

import { LanguageServiceDefaults } from './monaco.contribution';
import type { EscalierWorker } from './escalier-worker';
import { IDisposable, Uri, editor } from 'monaco-editor-core';

const STOP_WHEN_IDLE_FOR = 2 * 60 * 1000; // 2min

export class WorkerManager {
	private _defaults: LanguageServiceDefaults;
	private _idleCheckInterval: number;
	private _lastUsedTime: number;
	private _configChangeListener: IDisposable;

	private _worker: editor.MonacoWebWorker<EscalierWorker> | null;
	private _client: Promise<EscalierWorker> | null;

	constructor(defaults: LanguageServiceDefaults) {
		this._defaults = defaults;
		this._worker = null;
		this._client = null;
		this._idleCheckInterval = window.setInterval(() => this._checkIfIdle(), 30 * 1000);
		this._lastUsedTime = 0;
		this._configChangeListener = this._defaults.onDidChange(() => this._stopWorker());
	}

	private _stopWorker(): void {
		if (this._worker) {
			this._worker.dispose();
			this._worker = null;
		}
		this._client = null;
	}

	dispose(): void {
		clearInterval(this._idleCheckInterval);
		this._configChangeListener.dispose();
		this._stopWorker();
	}

	private _checkIfIdle(): void {
		if (!this._worker) {
			return;
		}
		const timePassedSinceLastUsed = Date.now() - this._lastUsedTime;
		if (timePassedSinceLastUsed > STOP_WHEN_IDLE_FOR) {
			this._stopWorker();
		}
	}

	private _getClient(): Promise<EscalierWorker> {
		this._lastUsedTime = Date.now();

		if (!this._client) {
			this._worker = editor.createWebWorker<EscalierWorker>({
				// module that exports the create() method and returns a `JSONWorker` instance
				moduleId: 'vs/language/json/jsonWorker',

				label: this._defaults.languageId,

				// passed in to the create() method
				createData: {
					languageSettings: this._defaults.diagnosticsOptions,
					languageId: this._defaults.languageId,
					enableSchemaRequest: this._defaults.diagnosticsOptions.enableSchemaRequest
				}
			});

			this._client = <Promise<EscalierWorker>>(<any>this._worker.getProxy());
		}

		return this._client;
	}

	getLanguageServiceWorker(...resources: Uri[]): Promise<EscalierWorker> {
		let _client: EscalierWorker;
		return this._getClient()
			.then((client) => {
				_client = client;
			})
			.then(() => {
				if (this._worker) {
					return this._worker.withSyncedResources(resources);
				}
			})
			.then(() => _client);
	}
}
