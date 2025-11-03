declare namespace Color {
  export type RGB = {};
  export const RGB: {new (r: number, g: number, b: number): Color, [Symbol.customMatcher](subject: RGB): [number, number, number]};
  export type Hex = {};
  export const Hex: {new (code: string): Color, [Symbol.customMatcher](subject: Hex): [string]};
}
declare type Color = Color.RGB | Color.Hex;
declare const color: Color;
declare const result: number | string;
