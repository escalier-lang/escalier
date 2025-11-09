export interface Extractor<TSubject, TReceiver> {
    [Symbol.customMatcher]: (
        subject: TSubject,
        mode: 'list',
        receiver: TReceiver,
    ) => object;
}

export function InvokeCustomMatcherOrThrow<TSubject, TReceiver>(
    extractor: Extractor<TSubject, TReceiver>,
    subject: TSubject,
    receiver: TReceiver,
) {
    if (!(extractor instanceof Object) || extractor === null) {
        throw new TypeError();
    }
    const f = extractor[Symbol.customMatcher];
    if (typeof f !== 'function') {
        throw new TypeError();
    }
    const result = f.apply(extractor, [subject, 'list', receiver]);
    if (typeof result !== 'object' || result === null) {
        throw new TypeError();
    }
    return result;
}
