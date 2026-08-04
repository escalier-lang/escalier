let temp1;
try {
  temp1 = "success";
} catch (__error) {
  temp1 = "error";
}
export const basicTryCatch = temp1;
let temp2;
try {
  const x = 5;
  temp2 = x + 10;
} catch (__error) {
  const y = 0;
  temp2 = y;
}
export const blockBody = temp2;
let temp3;
try {
  throw "x";
} catch (__error) {
  const e = __error;
  temp3 = e;
}
export const catchAllFirst = temp3;
export function guardedBinding(temp4) {
  const n = temp4;
  let temp5;
  try {
    throw {code: n};
  } catch (__error) {
    if (__error != null && "code" in __error && __error.code > 0) {
      const {code: c} = __error;
      temp5 = c;
    } else {
      temp5 = 0;
    }
  }
  return temp5;
}
export function guardedIdent(temp6) {
  const n = temp6;
  let temp7;
  try {
    throw "x";
  } catch (__error) {
    const e = __error;
    if (e == "x" && n > 0) {
      temp7 = 0;
    } else {
      throw __error;
    }
  }
  return temp7;
}
export function guardedRefutable(temp8) {
  const n = temp8;
  let temp9;
  try {
    throw "x";
  } catch (__error) {
    if (__error == "x" && n > 0) {
      temp9 = 0;
    } else {
      temp9 = 1;
    }
  }
  return temp9;
}
export function guardedWildcard(temp10) {
  const n = temp10;
  let temp11;
  try {
    throw "x";
  } catch (__error) {
    if (n > 0) {
      temp11 = 0;
    } else {
      throw __error;
    }
  }
  return temp11;
}
let temp12;
try {
  temp12 = 42;
} catch (__error) {
  temp12 = "error";
}
export const mixedReturn = temp12;
let temp13;
try {
  throw Error("fail");
} catch (__error) {
  const Error = __error;
  temp13 = "caught error";
}
export const multipleCases = temp13;
let temp14;
try {
  let temp15;
  try {
    temp15 = 5;
  } catch (__error) {
    temp15 = 10;
  }
  temp14 = temp15;
} catch (__error) {
  temp14 = 0;
}
export const nestedTryCatch = temp14;
let temp16;
try {
  throw {message: "fail"};
} catch (__error) {
  if (__error != null && "message" in __error) {
    const {message: msg} = __error;
    temp16 = msg;
  } else {
    temp16 = "unknown";
  }
}
export const objectPattern = temp16;
let temp17;
try {
  throw "fail";
} catch (__error) {
  const msg = __error;
  temp17 = msg;
}
export const patternBinding = temp17;
export function safeDivide(temp18, temp19) {
  const a = temp18;
  const b = temp19;
  let temp20;
  try {
    temp20 = a / b;
  } catch (__error) {
    temp20 = 0;
  }
  return temp20;
}
let temp21;
try {
  throw "error";
} catch (__error) {
  const msg = __error;
  temp21 = "caught: " + msg;
}
export const tryCatchWithThrow = temp21;
let temp22;
try {
  throw "critical";
} catch (__error) {
  const err = __error;
  if (err == "critical") {
    temp22 = "critical error";
  } else {
    temp22 = "other error";
  }
}
export const withGuard = temp22;
//# sourceMappingURL=./index.js.map
