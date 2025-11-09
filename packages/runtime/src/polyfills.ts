if (!Symbol.customMatcher) {
    Object.defineProperty(Symbol, 'customMatcher', {
        value: Symbol('Symbol.customMatcher'),
        writable: false,
        enumerable: false,
        configurable: false,
    });
}
