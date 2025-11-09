import { describe, test, expect } from 'vitest';

import { InvokeCustomMatcherOrThrow, type Extractor } from './extractor';

describe('Sample Test', () => {
    test('should pass', () => {
        expect(1 + 1).toBe(2);
    });

    test('const C(x) = subject', () => {
        class C {
            #data: string;
            constructor(data: string) {
                this.#data = data;
            }
            static [Symbol.customMatcher](subject: C) {
                return #data in subject && [subject.#data];
            }
        }

        const subject = new C('data');

        const [x] = InvokeCustomMatcherOrThrow(C, subject, undefined);

        expect(x).toBe('data');
    });

    test('const C(x, y) = subject', () => {
        class C {
            #first: number;
            #second: number;
            constructor(first: number, second: number) {
                this.#first = first;
                this.#second = second;
            }
            static [Symbol.customMatcher](subject: C) {
                return #first in subject && [subject.#first, subject.#second];
            }
        }

        const subject = new C(1, 2);

        const [x, y] = InvokeCustomMatcherOrThrow(C, subject, undefined);

        expect(x).toBe(1);
        expect(y).toBe(2);
    });

    test('const C(x, ...y) = subject', () => {
        class C {
            #first: number;
            #second: number;
            #third: number;
            constructor(first: number, second: number, third: number) {
                this.#first = first;
                this.#second = second;
                this.#third = third;
            }
            static [Symbol.customMatcher](subject: C) {
                return (
                    #first in subject && [
                        subject.#first,
                        subject.#second,
                        subject.#third,
                    ]
                );
            }
        }

        const subject = new C(1, 2, 3);

        const [x, ...y] = InvokeCustomMatcherOrThrow(C, subject, undefined);

        expect(x).toBe(1);
        expect(y).toEqual([2, 3]);
    });

    test('const C(x = -1, y) = subject', () => {
        class C {
            #first: number | undefined;
            #second: number;
            constructor(first: number | undefined, second: number) {
                this.#first = first;
                this.#second = second;
            }
            static [Symbol.customMatcher](subject: C) {
                return #first in subject && [subject.#first, subject.#second];
            }
        }

        const subject = new C(undefined, 2);

        const [x = -1, y] = InvokeCustomMatcherOrThrow(C, subject, undefined);

        expect(x).toBe(-1);
        expect(y).toBe(2);
    });

    test('const C({ x }) = subject', () => {
        class C {
            #data: { x: number; y: number };
            constructor(data: { x: number; y: number }) {
                this.#data = data;
            }
            static [Symbol.customMatcher](subject: C) {
                return #data in subject && [subject.#data];
            }
        }

        const subject = new C({ x: 1, y: 2 });

        const [{ x, y }] = InvokeCustomMatcherOrThrow(C, subject, undefined);

        expect(x).toBe(1);
        expect(y).toBe(2);
    });

    test('const C(D(x)) = subject', () => {
        class C {
            #data1: D;
            constructor(data1: D) {
                this.#data1 = data1;
            }
            static [Symbol.customMatcher](subject: C) {
                return #data1 in subject && [subject.#data1];
            }
        }

        class D {
            #data2: string;
            constructor(data2: string) {
                this.#data2 = data2;
            }
            static [Symbol.customMatcher](subject: D) {
                return #data2 in subject && [subject.#data2];
            }
        }

        const subject = new C(new D('data'));

        const [_a] = InvokeCustomMatcherOrThrow(C, subject, undefined);
        const [x] = InvokeCustomMatcherOrThrow(D, _a, undefined);

        expect(x).toBe('data');
    });
});
