import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export const shapes = {};
export function area(temp1) {
  const s = temp1;
  let temp2;
  let temp3;
  temp3 = s;
  if (temp3 instanceof shapes.Shape.Circle) {
    const [temp6] = InvokeCustomMatcherOrThrow(shapes.Shape.Circle, temp3, undefined);
    const r = temp6;
    temp2 = 3 * r * r;
  } else if (temp3 instanceof shapes.Shape.Rect) {
    const [temp4, temp5] = InvokeCustomMatcherOrThrow(shapes.Shape.Rect, temp3, undefined);
    const w = temp4;
    const h = temp5;
    temp2 = w * h;
  }
  return temp2;
}
const shapes__Shape = {};
class shapes__Shape__Circle {
  constructor(temp7) {
    const radius = temp7;
    this.radius = radius;
  }
  static [Symbol.customMatcher](subject) {
    return [subject.radius];
  }
}
shapes__Shape.Circle = shapes__Shape__Circle;
class shapes__Shape__Rect {
  constructor(temp8, temp9) {
    const width = temp8;
    const height = temp9;
    this.width = width;
    this.height = height;
  }
  static [Symbol.customMatcher](subject) {
    return [subject.width, subject.height];
  }
}
shapes__Shape.Rect = shapes__Shape__Rect;
shapes.Shape = shapes__Shape;
export const unit = area(new shapes.Shape.Rect(1, 1));
//# sourceMappingURL=./index.js.map
