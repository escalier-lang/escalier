import * as path from "path"
import { TextEncoder, TextDecoder } from "util";

globalThis.require = require
globalThis.path = path
globalThis.TextEncoder = TextEncoder;
globalThis.TextDecoder = TextDecoder;
