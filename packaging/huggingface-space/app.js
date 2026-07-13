const installs = {
  windows: {
    label: "PowerShell",
    command:
      "irm https://raw.githubusercontent.com/mclaut/ariadne/main/install.ps1 -OutFile install.ps1\n" +
      "powershell -ExecutionPolicy Bypass -File .\\install.ps1",
    note: "Explicitly choose Claude Code, Codex, both, or core-only during setup.",
  },
  linux: {
    label: "Shell",
    command:
      "curl -fsSL https://raw.githubusercontent.com/mclaut/ariadne/main/install.sh | sh",
    note: "Installs a loopback-only Qdrant user service and reuses the Ollama system service.",
  },
  macos: {
    label: "Terminal",
    command:
      "curl -fsSL https://raw.githubusercontent.com/mclaut/ariadne/main/install.sh | sh",
    note: "Native Intel and Apple Silicon builds with Ollama Metal acceleration.",
  },
};

const tabs = Array.from(document.querySelectorAll(".platform-tab"));
const command = document.querySelector("#install-command");
const commandLabel = document.querySelector("#command-label");
const installNote = document.querySelector("#install-note");
const copyButton = document.querySelector("#copy-command");
const copyLabel = document.querySelector("#copy-label");

function selectPlatform(platform) {
  const selected = installs[platform];
  if (!selected) return;

  tabs.forEach((tab) => {
    const active = tab.dataset.platform === platform;
    tab.classList.toggle("active", active);
    tab.setAttribute("aria-selected", String(active));
  });

  command.textContent = selected.command;
  commandLabel.textContent = selected.label;
  installNote.textContent = selected.note;
  copyLabel.textContent = "Copy";
}

tabs.forEach((tab) => {
  tab.addEventListener("click", () => selectPlatform(tab.dataset.platform));
});

copyButton.addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(command.textContent);
    copyLabel.textContent = "Copied";
    window.setTimeout(() => {
      copyLabel.textContent = "Copy";
    }, 1600);
  } catch {
    copyLabel.textContent = "Select text";
  }
});

if (window.lucide) {
  window.lucide.createIcons({ attrs: { "stroke-width": 2 } });
}
