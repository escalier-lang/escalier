export class C {
  constructor(temp1) {
    const _field1 = temp1;
    this[sym] = _field1;
  }
  get value() {
    return this[sym];
  }
}
export const c = new C(5);
export const r = c[sym];
export function store(temp2, temp3) {
  const c = temp2;
  const v = temp3;
  c[sym] = v;
}
//# sourceMappingURL=./index.js.map
