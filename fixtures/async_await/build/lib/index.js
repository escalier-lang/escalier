export async function conditionalFetch(temp1, temp2) {
  const useCache = temp1;
  const url = temp2;
  if (useCache) {
    return "cached data";
  } else {
    const data = await fetch(url);
    return data;
  }
}
export async function fetchData(temp3) {
  const url = temp3;
  return "data from " + url;
}
export async function fetchMultiple(temp4, temp5) {
  const url1 = temp4;
  const url2 = temp5;
  const data1 = await fetch(url1);
  const text1 = await data1.text();
  const data2 = await fetch(url2);
  const text2 = await data2.text();
  return text1 + " and " + text2;
}
export async function fetchWithAwait(temp6) {
  const url = temp6;
  const data = await fetch(url);
  return data;
}
export async function fetchWithError(temp7) {
  const shouldError = temp7;
  let temp8;
  if (shouldError) {
    throw "Something went wrong";
  }
  temp8;
  return "success";
}
//# sourceMappingURL=./index.js.map
