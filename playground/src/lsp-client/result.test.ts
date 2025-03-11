import { expect, test } from 'vitest';

import { Result } from './result';

test('Result.Ok', () => {
    const result = Result.Ok(42);
    expect(result.isOk).toBe(true);
    expect(result.isErr).toBe(false);
    expect(result.value).toBe(42);
});

test('Result.Err', () => {
    const result = Result.Err(new Error('error'));
    expect(result.isOk).toBe(false);
    expect(result.isErr).toBe(true);
    expect(result.error.message).toBe('error');
});
