declare type Shape = {area: number};
declare const Shape: {new (area: number): Shape};
declare type Box<T> = Shape;
declare namespace Color {
  export type RGB = {};
  export const RGB: {new (r: number, g: number, b: number): Color, [Symbol.customMatcher](subject: RGB): [number, number, number]};
  export type Hex = {};
  export const Hex: {new (code: string): Color, [Symbol.customMatcher](subject: Hex): [string]};
}
declare type Color = Color.RGB | Color.Hex;
declare type GeoCircle = geo.Circle;
declare type HexColor = Color.Hex;
declare type Holder<T> = {value: T};
declare const Holder: {new <T>(value: T): Holder<T>};
declare type NumberHolder = Holder<number>;
declare type Other = {tag: string};
declare const Other: {new (tag: string): Other};
declare type ShapeAlias = Shape;
declare type ShapeAliasAlias = ShapeAlias;
declare type ShapeOrOther = Shape | Other;
declare namespace geo {
  type Circle = {radius: number};
  const Circle: {new (radius: number): Circle};
  type CircleAlias = Circle;
}
