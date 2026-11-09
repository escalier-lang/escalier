/**
 * The types whose values are copied rather than shared. Escalier's built-in `ValueType`
 * bound renders as this alias in the `.d.ts` files the compiler generates.
 *
 * `Function` stands for Escalier's `function` type, which admits a class and a callable
 * object as well as a plain function.
 */
export type ValueType =
  | number
  | string
  | boolean
  | bigint
  | symbol
  | null
  | undefined
  | Function;
