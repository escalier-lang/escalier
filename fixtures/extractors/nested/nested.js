const subject = C(D("hello"), E([5, 10]));
const [temp1, temp2] = InvokeCustomMatcherOrThrow(C, subject, undefined);
const [temp3] = InvokeCustomMatcherOrThrow(D, temp1, undefined);
const msg = temp3;
const [temp4] = InvokeCustomMatcherOrThrow(E, temp2, undefined);
const [x, y] = temp4;
//# sourceMappingURL=./nested.esc.map
