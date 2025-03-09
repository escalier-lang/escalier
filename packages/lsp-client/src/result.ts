export type Result<T, E> = Ok<T> | Err<E>;

// Ok type for success result
class Ok<T> {
    readonly value: T;
    constructor(value: T) {
        this.value = value;
    }

    get isOk(): true {
        return true;
    }
    get isErr(): false {
        return false;
    }
}

// Err type for failure result
class Err<E> {
    readonly error: E;
    constructor(error: E) {
        this.error = error;
    }

    get isOk(): false {
        return false;
    }
    get isErr(): true {
        return true;
    }
}

export const Result = {
    Ok<T>(value: T): Ok<T> {
        return new Ok(value);
    },
    Err<E>(error: E): Err<E> {
        return new Err(error);
    },
};

export type AsyncResult<T, E> = Promise<Result<T, E>>;
