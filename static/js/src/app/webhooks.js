let webhooks = [];
let latestLoadRequest = 0;
let latestEditRequest = 0;

const setWebhookFormDisabled = (disabled) => {
    ["name", "url", "secret", "is_active", "modalSubmit"].forEach((id) => {
        document.getElementById(id).disabled = disabled;
    });
};

const dismiss = () => {
    latestEditRequest++;
    document.getElementById("name").value = "";
    document.getElementById("url").value = "";
    document.getElementById("secret").value = "";
    document.getElementById("is_active").checked = false;
    document.getElementById("flashes").replaceChildren();
};

const saveWebhook = (id) => {
    let wh = {
        name: document.getElementById("name").value,
        url: document.getElementById("url").value,
        secret: document.getElementById("secret").value,
        is_active: document.getElementById("is_active").checked,
    };
    if (id != -1) {
        wh.id = parseInt(id);
        api.webhookId.put(wh)
            .then(function(data) {
                dismiss();
                load();
                bsModalHide("#modal");
                successFlash(`Webhook "${escapeHtml(wh.name)}" has been updated successfully!`);
            }, function(error) {
                modalError(requestErrorMessage(error))
            })
    } else {
        api.webhooks.post(wh)
            .done(function(data) {
                load();
                dismiss();
                bsModalHide("#modal");
                successFlash(`Webhook "${escapeHtml(wh.name)}" has been created successfully!`);
            })
            .fail(function(data) {
                modalError(data.responseJSON.message)
            })
    }
};

const load = () => {
    const loadRequest = ++latestLoadRequest;
    document.getElementById("webhookTable").style.display = "none";
    document.getElementById("loading").style.display = "";
    api.webhooks.get()
        .then((whs) => {
            if (loadRequest !== latestLoadRequest) {
                return;
            }
            webhooks = whs;
            document.getElementById("loading").style.display = "none"
            document.getElementById("webhookTable").style.display = ""
            let webhookTable = new DataTable("#webhookTable", {
                destroy: true,
                columnDefs: [{
                    orderable: false,
                    targets: "no-sort"
                }]
            });
            webhookTable.clear();
            webhooks.forEach((webhook) => {
                webhookTable.row.add([
                    escapeHtml(webhook.name),
                    escapeHtml(webhook.url),
                    escapeHtml(webhook.is_active),
                    `
                      <div class="float-end">
                        <button class="btn btn-primary ping_button" data-webhook-id="${webhook.id}">
                          Ping
                        </button>
                        <button class="btn btn-primary edit_button" data-bs-toggle="modal" data-bs-target="#modal" data-webhook-id="${webhook.id}">
                          <i class="fa fa-pencil"></i>
                        </button>
                        <button class="btn btn-danger delete_button" data-webhook-id="${webhook.id}">
                          <i class="fa fa-trash-o"></i>
                        </button>
                      </div>
                    `
                ]).draw()
            })
        }, () => {
            if (loadRequest !== latestLoadRequest) {
                return;
            }
            errorFlash("Error fetching webhooks")
        })
};

// submitHandler holds the listener currently bound to the modal's submit
// button. The button is reused for every webhook, so the previous listener has
// to be removed or a single click would save more than once.
let submitHandler = null;

const editWebhook = (id) => {
    const editRequest = ++latestEditRequest;
    const submit = document.getElementById("modalSubmit");
    if (submitHandler) {
        submit.removeEventListener("click", submitHandler);
    }
    submitHandler = () => {
        saveWebhook(id);
    };
    submit.addEventListener("click", submitHandler);
    if (id !== -1) {
        document.getElementById("webhookModalLabel").textContent = "Edit Webhook"
        setWebhookFormDisabled(true);
        api.webhookId.get(id)
          .then(function(wh) {
              if (editRequest !== latestEditRequest) {
                  return;
              }
              document.getElementById("name").value = wh.name;
              document.getElementById("url").value = wh.url;
              document.getElementById("secret").value = wh.secret;
              document.getElementById("is_active").checked = wh.is_active;
              setWebhookFormDisabled(false);
          }, function () {
              if (editRequest !== latestEditRequest) {
                  return;
              }
              errorFlash("Error fetching webhook")
          });
    } else {
        document.getElementById("webhookModalLabel").textContent = "New Webhook"
        setWebhookFormDisabled(false);
    }
};

const deleteWebhook = (id) => {
    var wh = webhooks.find(x => x.id == id);
    if (!wh) {
        return;
    }
    Swal.fire({
        title: "Are you sure?",
        text: `This will delete the webhook '${escapeHtml(wh.name)}'`,
        type: "warning",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Delete",
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        preConfirm: function () {
            return new Promise((resolve, reject) => {
                api.webhookId.delete(id)
                    .done((msg) => {
                        resolve()
                    })
                    .fail((data) => {
                        reject(data.responseJSON.message)
                    })
            })
            .catch(error => {
                Swal.showValidationMessage(error)
              })
        }
    }).then(function(result) {
        if (result.value) {
            Swal.fire(
                "Webhook Deleted!",
                `The webhook has been deleted!`,
                "success"
            );
        }
        // Every button whose label contains "OK", which is what the previous
        // selector matched.
        document.querySelectorAll("button").forEach((button) => {
            if (button.textContent.includes("OK")) {
                button.addEventListener("click", function() {
                    location.reload();
                })
            }
        })
    })
};

const pingUrl = (btn, whId) => {
    dismiss();
    btn.disabled = true;
    api.webhookId.ping(whId)
        .then(function(wh) {
            btn.disabled = false;
            successFlash(`Ping of "${escapeHtml(wh.name)}" webhook succeeded.`);
        }, function(error) {
            btn.disabled = false;
            var wh = webhooks.find(x => x.id == whId);
            if (!wh) {
                return
            }
            errorFlash(`Ping of "${escapeHtml(wh.name)}" webhook failed: "${escapeHtml(requestErrorMessage(error))}"`)
        });
};

// The application scripts are plain classic scripts at the end of <body>, so
// the document is still parsing when they run and DOMContentLoaded has not
// fired yet.
document.addEventListener('DOMContentLoaded', function() {
    load();
    document.getElementById('modal').addEventListener('hide.bs.modal', function() {
        dismiss();
    });
    document.getElementById("new_button").addEventListener("click", function() {
        editWebhook(-1);
    });
    // Rows are rebuilt whenever the table is redrawn, so these stay delegated.
    // The handler receives the button that was clicked: under the previous
    // delegation that was what both `this` and `currentTarget` referred to,
    // whereas a native listener's currentTarget is the table itself.
    document.getElementById("webhookTable").addEventListener("click", function(e) {
        const button = e.target.closest(".edit_button, .delete_button, .ping_button");
        if (!button || !this.contains(button)) {
            return;
        }
        if (button.classList.contains("edit_button")) {
            editWebhook(button.getAttribute("data-webhook-id"));
            return;
        }
        if (button.classList.contains("delete_button")) {
            deleteWebhook(button.getAttribute("data-webhook-id"));
            return;
        }
        pingUrl(button, button.dataset.webhookId);
    });
});
