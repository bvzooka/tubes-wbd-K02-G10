/** @type {import('tailwindcss').Config} */
// Token desain diambil dari Milestone 1 (web/static/app.css). Penjelasan peran
// tiap token ada di frontend/DESIGN.md. Pakai nama token, jangan nilai mentah.
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        ink: { DEFAULT: '#0f1d15', 2: '#1b2b21' },
        text: '#15241b',
        muted: '#55675d',
        line: { DEFAULT: '#dbe6d9', strong: '#bccdb9' },
        paper: { DEFAULT: '#ffffff', soft: '#f6faf3' },
        green: { DEFAULT: '#167a42', hover: '#0f6636', soft: '#e3f3df', mid: '#bfe3b4' },
        btn: { DEFAULT: '#0b5a4a', hover: '#07473a' },
        lime: { DEFAULT: '#c3f26b', hover: '#d3f78f' },
        butter: { DEFAULT: '#f6eaa6', ink: '#6b5a00' },
        danger: { DEFAULT: '#b42318', soft: '#fdecea', ink: '#6e1d16' },
        warn: { DEFAULT: '#b54708', soft: '#fff8e6', ink: '#6b3d06' },
      },
      borderRadius: {
        md: '14px',
        lg: '20px',
        xl: '28px',
        '2xl': '36px',
      },
      boxShadow: {
        sm: '0 1px 2px rgb(15 29 21 / 6%)',
        card: '0 1px 2px rgb(15 29 21 / 5%), 0 10px 30px -14px rgb(15 29 21 / 18%)',
        lift: '0 2px 4px rgb(15 29 21 / 8%), 0 22px 40px -16px rgb(15 29 21 / 35%)',
        press: '0 8px 16px -8px rgb(15 29 21 / 50%)',
        focus: '0 0 0 3px #fff, 0 0 0 5px #167a42',
      },
      fontFamily: {
        sans: ['system-ui', '-apple-system', '"Segoe UI"', 'Roboto', 'sans-serif'],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Consolas', 'monospace'],
      },
      maxWidth: {
        content: '1240px',
      },
    },
  },
  plugins: [],
}
