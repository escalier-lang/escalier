import type { DownloadProgress } from '../downloads';

import styles from './loading-screen.module.css';

/** The step of start-up the loading screen reports. */
export type LoadingStatus =
    | { kind: 'downloading'; progress: DownloadProgress }
    | { kind: 'starting' }
    | { kind: 'failed'; message: string };

type LoadingScreenProps = {
    status: LoadingStatus;
};

/** Formats a byte count in megabytes with one decimal place, such as `2.4 MB`. */
export function formatMegabytes(bytes: number): string {
    return `${(bytes / 1_000_000).toFixed(1)} MB`;
}

/**
 * Returns the percentage of the download that has arrived, from 0 to 100, or
 * undefined when no download has a known size.
 */
export function percentLoaded(progress: DownloadProgress): number | undefined {
    if (progress.total === 0) {
        return undefined;
    }
    return Math.floor((progress.loaded / progress.total) * 100);
}

/**
 * Draws an inert outline of the playground's layout under a modal that reports how
 * start-up is going.
 */
export const LoadingScreen = ({ status }: LoadingScreenProps) => {
    let percent: number | undefined;
    let caption: string;
    switch (status.kind) {
        case 'downloading':
            percent = percentLoaded(status.progress);
            caption =
                percent === undefined
                    ? 'Downloading the language server and standard library…'
                    : `Downloading the language server and standard library: ${formatMegabytes(status.progress.loaded)} of ${formatMegabytes(status.progress.total)}`;
            break;
        case 'starting':
            percent = 100;
            caption = 'Starting the language server…';
            break;
        case 'failed':
            percent = undefined;
            caption = `Failed to load the playground: ${status.message}`;
            break;
    }

    return (
        <div className={styles.root}>
            <div className={styles.skeleton} aria-hidden="true" inert>
                <div className={styles.toolbar} />
                <div className={styles.explorer}>
                    <div className={styles.line} style={{ width: '50%' }} />
                    <div className={styles.line} style={{ width: '70%' }} />
                    <div className={styles.line} style={{ width: '60%' }} />
                </div>
                <div className={styles.inputTabs} />
                <div className={styles.outputTabs} />
                <div className={styles.input}>
                    <div className={styles.line} style={{ width: '40%' }} />
                    <div className={styles.line} style={{ width: '65%' }} />
                    <div className={styles.line} style={{ width: '55%' }} />
                </div>
                <div className={styles.output} />
            </div>
            <div className={styles.backdrop}>
                <dialog
                    open
                    className={styles.modal}
                    aria-modal="true"
                    aria-labelledby="loading-title"
                >
                    <h2 id="loading-title" className={styles.title}>
                        Loading the Escalier playground
                    </h2>
                    {/* A progress element without a value reads as indeterminate. */}
                    <progress
                        className={styles.visuallyHidden}
                        aria-labelledby="loading-title"
                        max={100}
                        value={percent}
                    />
                    <div className={styles.track} aria-hidden="true">
                        <div
                            className={
                                status.kind === 'failed'
                                    ? styles.failedBar
                                    : percent === undefined
                                      ? styles.indeterminateBar
                                      : styles.bar
                            }
                            style={
                                percent === undefined
                                    ? undefined
                                    : { width: `${percent}%` }
                            }
                        />
                    </div>
                    <p
                        className={
                            status.kind === 'failed'
                                ? styles.error
                                : styles.caption
                        }
                        role={status.kind === 'failed' ? 'alert' : undefined}
                    >
                        {caption}
                    </p>
                </dialog>
            </div>
        </div>
    );
};
