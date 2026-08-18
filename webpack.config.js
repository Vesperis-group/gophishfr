"use strict";

const path = require("node:path");

module.exports = {
  mode: "production",
  target: "web",
  devtool: false,
  context: path.resolve(__dirname, "static", "js", "src", "app"),
  entry: {
    passwords: "./passwords.js",
  },
  output: {
    path: path.resolve(__dirname, "static", "js", "dist", "app"),
    filename: "[name].min.js",
    clean: false,
  },
  optimization: {
    moduleIds: "deterministic",
    chunkIds: "deterministic",
  },
};