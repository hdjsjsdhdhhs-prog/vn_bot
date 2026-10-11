// Vite `?raw` imports (used to load Go-rendered page fixtures in tests).
declare module '*?raw' {
  const content: string;
  export default content;
}
