export declare async function fetchData(url: string): Promise<string, never>;
declare function fetch(url: string): Promise<string, Error>;
export declare async function fetchWithAwait(url: string): Promise<string, string>;
export declare async function fetchWithError(shouldError: boolean): Promise<"success", "Something went wrong">;
export declare async function fetchMultiple(url1: string, url2: string): Promise<string, Error | Error>;
export declare async function conditionalFetch(useCache: boolean, url: string): Promise<"cached data" | string, Error>;
