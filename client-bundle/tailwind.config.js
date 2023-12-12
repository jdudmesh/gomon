/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./src/**/*.ts", "*.ts", "./*.html", "../**/*.templ"],
  darkMode: "class",
  theme: {
    extend: {}
  },
  plugins: [require("daisyui")]
};
