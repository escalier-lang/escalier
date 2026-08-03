let temp1;
try {
  temp1 = "success";
} catch (__error) {
  if (true) {
    temp1 = "error";
  }
}
export const basicTryCatch = temp1;
let temp2;
try {
  const x = 5;
  temp2 = x + 10;
} catch (__error) {
  if (true) {
    const y = 0;
    temp2 = y;
  }
}
export const blockBody = temp2;
let temp3;
try {
  throw "x";
} catch (__error) {
  if (true) {
    const e = __error;
    temp3 = e;
  } else if (__error == "x") {
    temp3 = "unreachable";
  }
}
export const catchAllFirst = temp3;
export function guardedIdent(temp4) {
  const n = temp4;
  let temp5;
  try {
    throw "x";
  } catch (__error) {
    if (true) {
      const e = __error;
      if (e == "x" && n > 0) {
        temp5 = 0;
      } else {
        throw __error;
      }
    }
  }
  return temp5;
}
export function guardedRefutable(temp6) {
  const n = temp6;
  let temp7;
  try {
    throw "x";
  } catch (__error) {
    if (__error == "x") {
      if (n > 0) {
        temp7 = 0;
      } else if (true) {
        temp7 = 1;
      }
    } else {
      temp7 = 1;
    }
  }
  return temp7;
}
export function guardedWildcard(temp8) {
  const n = temp8;
  let temp9;
  try {
    throw "x";
  } catch (__error) {
    if (true) {
      if (n > 0) {
        temp9 = 0;
      } else {
        throw __error;
      }
    }
  }
  return temp9;
}
let temp10;
try {
  temp10 = 42;
} catch (__error) {
  if (true) {
    temp10 = "error";
  }
}
export const mixedReturn = temp10;
let temp11;
try {
  throw Error("fail");
} catch (__error) {
  if (true) {
    const Error = __error;
    temp11 = "caught error";
  } else {
    temp11 = "unknown";
  }
}
export const multipleCases = temp11;
let temp12;
try {
  let temp13;
  try {
    temp13 = 5;
  } catch (__error) {
    if (true) {
      temp13 = 10;
    }
  }
  temp12 = temp13;
} catch (__error) {
  if (true) {
    temp12 = 0;
  }
}
export const nestedTryCatch = temp12;
let temp14;
try {
  throw {message: "fail"};
} catch (__error) {
  if (__error != null && "message" in __error) {
    const {message: msg} = __error;
    temp14 = msg;
  } else {
    temp14 = "unknown";
  }
}
export const objectPattern = temp14;
let temp15;
try {
  throw "fail";
} catch (__error) {
  if (true) {
    const msg = __error;
    temp15 = msg;
  } else {
    temp15 = "unknown";
  }
}
export const patternBinding = temp15;
export function safeDivide(temp16, temp17) {
  const a = temp16;
  const b = temp17;
  let temp18;
  try {
    temp18 = a / b;
  } catch (__error) {
    if (true) {
      temp18 = 0;
    }
  }
  return temp18;
}
let temp19;
try {
  throw "error";
} catch (__error) {
  if (true) {
    const msg = __error;
    temp19 = "caught: " + msg;
  }
}
export const tryCatchWithThrow = temp19;
let temp20;
try {
  throw "critical";
} catch (__error) {
  if (true) {
    const err = __error;
    if (err == "critical") {
      temp20 = "critical error";
    } else if (true) {
      temp20 = "other error";
    }
  }
}
export const withGuard = temp20;
//# sourceMappingURL=./index.js.map
