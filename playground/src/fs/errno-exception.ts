export class ErrnoException extends Error implements NodeJS.ErrnoException {
    /**
     * Optional error code (e.g. `'ENOENT'`), often a string but can also be a number.
     */
    code?: string;

    /**
     * The raw error number (e.g. `-2` for `ENOENT`). Mirrors Node’s `errno` property.
     */
    errno?: number;

    /**
     * The system call that generated the error (e.g. `'open'`, `'unlink'`).
     */
    syscall?: string;

    /**
     * The path related to the operation that caused the error, if applicable.
     */
    path?: string;

    /**
     * Create an `ErrnoException` that behaves like the native Node error.
     *
     * @param message Human‑readable error message.
     * @param opts   Optional properties matching Node’s `ErrnoException`.
     */
    constructor(
        message: string,
        opts?: {
            code?: string;
            errno?: number;
            syscall?: string;
            path?: string;
        },
    ) {
        super(message);
        // Preserve the name “Error” (Node’s ErrnoException inherits from Error)
        this.name = 'Error';

        // Assign any optional properties passed in.
        if (opts) {
            this.code = opts.code;
            this.errno = opts.errno;
            this.syscall = opts.syscall;
            this.path = opts.path;
        }

        // Ensure the prototype chain is correct for `instanceof ErrnoException`.
        Object.setPrototypeOf(this, new.target.prototype);
    }
}
