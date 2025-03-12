export class Deferred {
    promise: Promise<any>;
    // @ts-expect-error: resolve is set in the constructor
    resolve: (value?: any) => void;
    // @ts-expect-error: reject is set in the constructor
    reject: (reason?: any) => void;

    constructor() {
        this.promise = new Promise((resolve, reject) => {
            this.resolve = resolve;
            this.reject = reject;
        });
    }
}
