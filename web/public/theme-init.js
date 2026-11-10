(function () {
  try {
    var t = localStorage.getItem("mf-color-scheme");
    if (t !== "light" && t !== "dark") t = "dark";
    document.documentElement.setAttribute("data-mantine-color-scheme", t);
  } catch (e) {
    /* storage unavailable */
  }
})();
