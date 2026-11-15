export declare function area(s: shapes.Shape): number;
export declare const unit: number;
declare namespace shapes {
  export namespace Shape {
    export type Circle = {};
    export const Circle: {new (radius: number): shapes.Shape, [Symbol.customMatcher](subject: Circle): [number]};
    export type Rect = {};
    export const Rect: {new (width: number, height: number): shapes.Shape, [Symbol.customMatcher](subject: Rect): [number, number]};
  }
  export type Shape = Shape.Circle | Shape.Rect;
}
