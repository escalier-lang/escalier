export class Deferred<T, E> {
    public readonly promise: Promise<T>;
    // @ts-ignore
    public resolve: (value: T) => void;
    // @ts-ignore
    public reject: (error: E) => void;

    constructor() {
        this.promise = new Promise((resolve, reject) => {
            this.resolve = resolve;
            this.reject = reject;
        });
    }
}
