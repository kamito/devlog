let count = 0;

document.getElementById("btn").addEventListener("click", () => {
  count++;
  document.getElementById("count").textContent = count;
});
