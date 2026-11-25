package documents

// builtinDocument is an Amazon-owned document seeded at startup.
type builtinDocument struct {
	Name    string
	Type    string
	Content string
}

// Builtin document names referenced by other packages.
const (
	DocRunShellScript      = "AWS-RunShellScript"
	DocRunPowerShellScript = "AWS-RunPowerShellScript"
	DocSessionShell        = "SSM-SessionManagerRunShell"
	DocStartInteractive    = "AWS-StartInteractiveCommand"
	DocStartNonInteractive = "AWS-StartNonInteractiveCommand"
)

// builtins mirrors the most commonly used AWS-managed documents. Content is
// simplified but schema-compatible with the originals.
var builtins = []builtinDocument{
	{
		Name: DocRunShellScript, Type: TypeCommand,
		Content: `{
  "schemaVersion": "2.2",
  "description": "Run a shell script or specify the commands to run.",
  "parameters": {
    "commands": {
      "type": "StringList",
      "description": "(Required) Specify a shell script or a command to run."
    },
    "workingDirectory": {
      "type": "String",
      "default": "",
      "description": "(Optional) The path to the working directory on your instance."
    },
    "executionTimeout": {
      "type": "String",
      "default": "3600",
      "description": "(Optional) The time in seconds for a command to complete before it is considered to have failed. Default is 3600 (1 hour). Maximum is 172800 (48 hours).",
      "allowedPattern": "([1-9][0-9]{0,4})|(1[0-6][0-9]{4})|(17[0-1][0-9]{3})|(172[0-7][0-9]{2})|(172800)"
    }
  },
  "mainSteps": [
    {
      "action": "aws:runShellScript",
      "name": "runShellScript",
      "inputs": {
        "runCommand": "{{ commands }}",
        "workingDirectory": "{{ workingDirectory }}",
        "timeoutSeconds": "{{ executionTimeout }}"
      }
    }
  ]
}`,
	},
	{
		Name: DocRunPowerShellScript, Type: TypeCommand,
		Content: `{
  "schemaVersion": "2.2",
  "description": "Run a PowerShell script or specify the paths to scripts to run.",
  "parameters": {
    "commands": {
      "type": "StringList",
      "description": "(Required) Specify the commands to run or the paths to existing scripts on the instance."
    },
    "workingDirectory": {
      "type": "String",
      "default": "",
      "description": "(Optional) The path to the working directory on your instance."
    },
    "executionTimeout": {
      "type": "String",
      "default": "3600",
      "description": "(Optional) The time in seconds for a command to be completed before it is considered to have failed."
    }
  },
  "mainSteps": [
    {
      "action": "aws:runPowerShellScript",
      "name": "runPowerShellScript",
      "inputs": {
        "runCommand": "{{ commands }}",
        "workingDirectory": "{{ workingDirectory }}",
        "timeoutSeconds": "{{ executionTimeout }}"
      }
    }
  ]
}`,
	},
	{
		Name: DocSessionShell, Type: TypeSession,
		Content: `{
  "schemaVersion": "1.0",
  "description": "Document to hold regional settings for Session Manager",
  "sessionType": "Standard_Stream",
  "inputs": {
    "idleSessionTimeout": "20",
    "maxSessionDuration": "",
    "runAsEnabled": false,
    "runAsDefaultUser": "",
    "shellProfile": {"windows": "", "linux": ""}
  }
}`,
	},
	{
		Name: DocStartInteractive, Type: TypeSession,
		Content: `{
  "schemaVersion": "1.0",
  "description": "Document to run single interactive command on an instance",
  "sessionType": "InteractiveCommands",
  "parameters": {
    "command": {"type": "String", "description": "The command to run on the instance"}
  },
  "properties": {
    "windows": {"commands": "{{command}}", "runAsElevated": false},
    "linux": {"commands": "{{command}}", "runAsElevated": false},
    "macos": {"commands": "{{command}}", "runAsElevated": false}
  }
}`,
	},
	{
		Name: DocStartNonInteractive, Type: TypeSession,
		Content: `{
  "schemaVersion": "1.0",
  "description": "Document to run single non-interactive command on an instance",
  "sessionType": "NonInteractiveCommands",
  "parameters": {
    "command": {"type": "String", "description": "The command to run on the instance"}
  },
  "properties": {
    "windows": {"commands": "{{command}}", "runAsElevated": false},
    "linux": {"commands": "{{command}}", "runAsElevated": false},
    "macos": {"commands": "{{command}}", "runAsElevated": false}
  }
}`,
	},
}
