/// <reference types="vite/client" />
interface ImportMetaEnv {
  // BCP 47 tag for numbers and dates (e.g. "es-ES"); unset = the browser's locale.
  readonly VITE_CONSOLE_LOCALE?: string;
}
