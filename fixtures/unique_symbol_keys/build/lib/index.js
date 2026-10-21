const temp1 = sym;
export class C {
  constructor(temp2) {
    const _field1 = temp2;
    this[temp1] = _field1;
  }
  get value() {
    return this[sym];
  }
}
export const c = new C(5);
export const r = c[sym];
export function store(temp3, temp4) {
  const c = temp3;
  const v = temp4;
  c[sym] = v;
}
//# sourceMappingURL=./index.js.map
