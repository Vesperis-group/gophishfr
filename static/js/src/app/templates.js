// The previous selector escaped the dot in this id, which defeats jQuery's
// getElementById fast path, so it matched every element carrying the id rather
// than the first.
function clearModalFlashes() {
    document.querySelectorAll('[id="modal.flashes"]').forEach(function (container) {
        container.replaceChildren()
    })
}

// submitHandler holds the listener currently bound to the modal's submit
// button, which is reused for every record.
var submitHandler = null

function bindModalSubmit(handler) {
    // More than one element can carry the id "modalSubmit", one per modal. The
    // previous selector took jQuery's getElementById fast path and so bound
    // only the first, which is the page modal's own button.
    var submit = document.getElementById("modalSubmit")
    if (submitHandler) {
        submit.removeEventListener("click", submitHandler)
    }
    submitHandler = handler
    submit.addEventListener("click", submitHandler)
}

var templates = []
var htmlEditor
var latestTemplateRequest = 0
var latestTemplateLoadRequest = 0
var saveRequest = null
var activeTemplateId = null
var refreshTemplateModal = null
var latestImportRequest = 0
var importEmailRequest = null
var pendingAttachmentReads = 0
// attachmentsTable holds the DataTables instance backing the modal's attachment
// list. It is recreated every time the modal opens, which is what the destroy
// option did before.
var attachmentsTable = null
var icons = {
    "application/vnd.ms-excel": "fa-file-excel-o",
    "text/plain": "fa-file-text-o",
    "image/gif": "fa-file-image-o",
    "image/png": "fa-file-image-o",
    "application/pdf": "fa-file-pdf-o",
    "application/x-zip-compressed": "fa-file-archive-o",
    "application/x-gzip": "fa-file-archive-o",
    "application/vnd.openxmlformats-officedocument.presentationml.presentation": "fa-file-powerpoint-o",
    "application/vnd.openxmlformats-officedocument.wordprocessingml.document": "fa-file-word-o",
    "application/octet-stream": "fa-file-o",
    "application/x-msdownload": "fa-file-o"
}

function setTemplateFormDisabled(disabled) {
    ["name", "subject", "envelope-sender", "text_editor", "use_tracker_checkbox",
        "attachmentUpload"]
        .forEach(function (id) {
            document.getElementById(id).disabled = disabled
        })
    document.getElementById("modalSubmit").disabled = disabled || pendingAttachmentReads > 0
    document.querySelector('#modal button[onclick^="bsModalShow(\'#importEmailModal\'"]').disabled = disabled
    var codeEditor = document.querySelector("#modal .gophish-code-editor")
    if (codeEditor) {
        codeEditor.inert = disabled
    }
}

function enableTemplateFormAfterPendingSave(templateRequest) {
    if (!saveRequest) {
        setTemplateFormDisabled(false)
        return
    }
    var pendingSave = saveRequest
    setTemplateFormDisabled(true)
    var enableCurrentForm = function () {
        if (templateRequest == latestTemplateRequest) {
            setTemplateFormDisabled(false)
        }
    }
    pendingSave.then(enableCurrentForm, enableCurrentForm)
}

function setImportEmailFormDisabled(disabled) {
    document.getElementById("email_content").disabled = disabled
    document.getElementById("convert_links_checkbox").disabled = disabled
    document.querySelector("#importEmailModal #modalSubmit").disabled = disabled
}

function enableImportEmailAfterPendingRequest(importRequest) {
    if (!importEmailRequest) {
        setImportEmailFormDisabled(false)
        return
    }
    var pendingImport = importEmailRequest
    setImportEmailFormDisabled(true)
    var enableCurrentForm = function () {
        if (importRequest == latestImportRequest) {
            setImportEmailFormDisabled(false)
        }
    }
    pendingImport.then(enableCurrentForm, enableCurrentForm)
}

// Save attempts to POST to /templates/
function save(templateId) {
    if (saveRequest || pendingAttachmentReads > 0) {
        return
    }
    var template = {
        attachments: []
    }
    template.name = document.getElementById("name").value
    template.subject = document.getElementById("subject").value
    template.envelope_sender = document.getElementById("envelope-sender").value
    template.html = htmlEditor.getData()
    // If the "Add Tracker Image" checkbox is checked, add the tracker
    if (document.getElementById("use_tracker_checkbox").checked) {
        if (template.html.indexOf("{{.Tracker}}") == -1 &&
            template.html.indexOf("{{.TrackingUrl}}") == -1) {
            template.html = template.html.replace("</body>", "{{.Tracker}}</body>")
        }
    } else {
        // Otherwise, remove the tracker
        template.html = template.html.replace("{{.Tracker}}</body>", "</body>")
    }
    template.text = document.getElementById("text_editor").value
    // Add the attachments
    attachmentsTable.rows().data().toArray().forEach(function (target) {
        template.attachments.push({
            name: unescapeHtml(target[1]),
            content: target[3],
            type: target[4],
        })
    })

    var saveContext = latestTemplateRequest
    setTemplateFormDisabled(true)
    if (templateId != -1) {
        template.id = templateId
        saveRequest = api.templateId.put(template)
        saveRequest
            .then(function () {
                saveRequest = null
                successFlash("Template edited successfully!")
                load()
                if (saveContext == latestTemplateRequest) {
                    dismiss()
                } else if (activeTemplateId == template.id && refreshTemplateModal) {
                    var savedTemplate = templates.find(function (candidate) {
                        return candidate.id == template.id
                    })
                    if (savedTemplate) {
                        Object.assign(savedTemplate, template)
                    }
                    attachmentsTable.clear().draw()
                    refreshTemplateModal()
                }
            }, function (error) {
                saveRequest = null
                if (saveContext == latestTemplateRequest) {
                    setTemplateFormDisabled(false)
                    modalError(requestErrorMessage(error))
                }
            })
    } else {
        // Submit the template
        saveRequest = api.templates.post(template)
        saveRequest
            .then(function () {
                saveRequest = null
                successFlash("Template added successfully!")
                load()
                if (saveContext == latestTemplateRequest) {
                    dismiss()
                }
            }, function (error) {
                saveRequest = null
                if (saveContext == latestTemplateRequest) {
                    setTemplateFormDisabled(false)
                    modalError(requestErrorMessage(error))
                }
            })
    }
}

function dismiss() {
    latestTemplateRequest++
    activeTemplateId = null
    refreshTemplateModal = null
    pendingAttachmentReads = 0
    clearModalFlashes()
    if (attachmentsTable) {
        attachmentsTable.clear().draw()
    }
    document.getElementById("name").value = ""
    document.getElementById("subject").value = ""
    document.getElementById("text_editor").value = ""
    document.getElementById("html_editor").value = ""
    if (htmlEditor) {
        htmlEditor.setData("")
        htmlEditor.showSource()
    }
    bsModalHide("#modal")
}

var deleteTemplate = function (idx) {
    var templateId = templates[idx].id
    var templateName = templates[idx].name
    var deleteRequest = null
    Swal.fire({
        title: "Are you sure?",
        text: "This will delete the template. This can't be undone!",
        type: "warning",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Delete " + escapeHtml(templateName),
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        preConfirm: function () {
            if (deleteRequest) {
                return deleteRequest
            }
            deleteRequest = api.templateId.delete(templateId)
            return deleteRequest.then(function (response) {
                return response
            }, function (error) {
                deleteRequest = null
                Swal.showValidationMessage(requestErrorMessage(error))
            })
        }
    }).then(function (result) {
        if(result.value) {
            Swal.fire(
                'Template Deleted!',
                'This template has been deleted!',
                'success'
            );
        }
        // Every button whose label contains "OK", which is what the previous
        // selector matched.
        document.querySelectorAll("button").forEach(function (button) {
            if (button.textContent.includes("OK")) {
                button.addEventListener('click', function () {
                    location.reload()
                })
            }
        })
    })
}

// The attachment table's configuration is shared by every entry point that
// opens the modal, so the three paths cannot drift apart.
var attachmentsTableOptions = {
    destroy: true,
    order: [
        [1, "asc"]
    ],
    columnDefs: [{
        orderable: false,
        targets: "no-sort"
    }, {
        className: "datatable_hidden",
        targets: [3, 4]
    }]
}

function createAttachmentsTable() {
    attachmentsTable = new DataTable("#attachmentsTable", attachmentsTableOptions)
    return attachmentsTable
}

function attach(files) {
    // The table is deliberately not rebuilt here. It already holds the
    // template's attachments, and rebuilding it drops every row that has no
    // rendered node, which silently loses attachments once a template has more
    // than one page of them.
    if (!attachmentsTable) {
        createAttachmentsTable()
    }
    var attachmentContext = latestTemplateRequest
    var targetTable = attachmentsTable
    Array.prototype.forEach.call(files, function (file) {
        var reader = new FileReader();
        pendingAttachmentReads++
        document.getElementById("modalSubmit").disabled = true
        var finishRead = function () {
            if (attachmentContext != latestTemplateRequest || targetTable != attachmentsTable) {
                return false
            }
            pendingAttachmentReads--
            if (pendingAttachmentReads == 0 && !saveRequest) {
                document.getElementById("modalSubmit").disabled = false
            }
            return true
        }
        /* Make this a datatable */
        reader.onload = function (e) {
            if (!finishRead()) {
                return
            }
            var icon = icons[file.type] || "fa-file-o"
            // Add the record to the modal
            targetTable.row.add([
                '<i class="fa ' + icon + '"></i>',
                escapeHtml(file.name),
                '<span class="remove-row"><i class="fa fa-trash-o"></i></span>',
                reader.result.split(",")[1],
                file.type || "application/octet-stream"
            ]).draw()
        }
        reader.onerror = function (e) {
            if (!finishRead()) {
                return
            }
            console.log(e)
        }
        reader.readAsDataURL(file)
    })
}

function edit(idx) {
    var templateRequest = ++latestTemplateRequest
    var templateId = idx == -1 ? null : templates[idx].id
    activeTemplateId = templateId
    refreshTemplateModal = activeTemplateId == null ? null : function () {
        var refreshedIndex = templates.findIndex(function (template) {
            return template.id == activeTemplateId
        })
        if (refreshedIndex != -1) {
            edit(refreshedIndex)
        }
    }
    bindModalSubmit(function () {
        save(templateId == null ? -1 : templateId)
    })
    bindAttachmentUploadReset()
    htmlEditor = GophishHTMLEditor.create("html_editor")
    document.getElementById("attachmentsTable").style.display = ""
    createAttachmentsTable()
    var template = {
        attachments: []
    }
    if (idx != -1) {
        document.getElementById("templateModalLabel").textContent = "Edit Template"
        template = templates[idx]
        document.getElementById("name").value = template.name
        document.getElementById("subject").value = template.subject
        document.getElementById("envelope-sender").value = template.envelope_sender
        htmlEditor.setData(template.html)
        document.getElementById("text_editor").value = template.text
        attachmentRows = []
        template.attachments.forEach(function (file) {
            var icon = icons[file.type] || "fa-file-o"
            // Add the record to the modal
            attachmentRows.push([
                '<i class="fa ' + icon + '"></i>',
                escapeHtml(file.name),
                '<span class="remove-row"><i class="fa fa-trash-o"></i></span>',
                file.content,
                file.type || "application/octet-stream"
            ])
        })
        attachmentsTable.rows.add(attachmentRows).draw()
        if (template.html.indexOf("{{.Tracker}}") != -1) {
            document.getElementById("use_tracker_checkbox").checked = true
        } else {
            document.getElementById("use_tracker_checkbox").checked = false
        }

    } else {
        document.getElementById("templateModalLabel").textContent = "New Template"
        htmlEditor.setData("")
    }
    htmlEditor.showSource()
    enableTemplateFormAfterPendingSave(templateRequest)
}

function copy(idx) {
    var templateRequest = ++latestTemplateRequest
    activeTemplateId = null
    refreshTemplateModal = null
    bindModalSubmit(function () {
        save(-1)
    })
    bindAttachmentUploadReset()
    htmlEditor = GophishHTMLEditor.create("html_editor")
    document.getElementById("attachmentsTable").style.display = ""
    createAttachmentsTable()
    var template = {
        attachments: []
    }
    template = templates[idx]
    document.getElementById("name").value = "Copy of " + template.name
    document.getElementById("subject").value = template.subject
    document.getElementById("envelope-sender").value = template.envelope_sender
    htmlEditor.setData(template.html)
    htmlEditor.showSource()
    document.getElementById("text_editor").value = template.text
    template.attachments.forEach(function (file) {
        var icon = icons[file.type] || "fa-file-o"
        // Add the record to the modal
        attachmentsTable.row.add([
            '<i class="fa ' + icon + '"></i>',
            escapeHtml(file.name),
            '<span class="remove-row"><i class="fa fa-trash-o"></i></span>',
            file.content,
            file.type || "application/octet-stream"
        ]).draw()
    })
    if (template.html.indexOf("{{.Tracker}}") != -1) {
        document.getElementById("use_tracker_checkbox").checked = true
    } else {
        document.getElementById("use_tracker_checkbox").checked = false
    }
    enableTemplateFormAfterPendingSave(templateRequest)
}

function importEmail() {
    if (importEmailRequest) {
        return
    }
    var raw = document.getElementById("email_content").value
    var convert_links = document.getElementById("convert_links_checkbox").checked
    if (!raw) {
        modalError("No Content Specified!")
    } else {
        var importContext = latestImportRequest
        setImportEmailFormDisabled(true)
        importEmailRequest = api.import_email({
            content: raw,
            convert_links: convert_links
        })
        importEmailRequest
            .then(function (data) {
                importEmailRequest = null
                if (importContext != latestImportRequest) {
                    return
                }
                document.getElementById("text_editor").value = data.text
                htmlEditor.setData(data.html)
                document.getElementById("subject").value = data.subject
                // If the HTML is provided, let's open that view in the editor
                if (data.html) {
                    htmlEditor.showPreview()
                    document.querySelector('.nav-tabs a[href="#html"]').click()
                }
                bsModalHide("#importEmailModal")
            }, function (error) {
                importEmailRequest = null
                if (importContext != latestImportRequest) {
                    return
                }
                setImportEmailFormDisabled(false)
                modalError(requestErrorMessage(error))
            })
    }
}

function load() {
    var loadRequest = ++latestTemplateLoadRequest
    document.getElementById("templateTable").style.display = "none"
    document.getElementById("emptyMessage").style.display = "none"
    document.getElementById("loading").style.display = ""
    api.templates.get()
        .then(function (ts) {
            if (loadRequest != latestTemplateLoadRequest) {
                return
            }
            templates = ts
            document.getElementById("loading").style.display = "none"
            if (templates.length > 0) {
                document.getElementById("templateTable").style.display = ""
                templateTable = new DataTable("#templateTable", {
                    destroy: true,
                    columnDefs: [{
                        orderable: false,
                        targets: "no-sort"
                    }]
                });
                templateTable.clear()
                templateRows = []
                templates.forEach(function (template, i) {
                    templateRows.push([
                        escapeHtml(template.name),
                        moment(template.modified_date).format('MMMM Do YYYY, h:mm:ss a'),
                        "<div class='float-end'><span data-bs-toggle='modal' data-bs-target='#modal'><button class='btn btn-primary' data-bs-toggle='tooltip' data-bs-placement='left' title='Edit Template' onclick='edit(" + i + ")'>\
                    <i class='fa fa-pencil'></i>\
                    </button></span>\
		    <span data-bs-toggle='modal' data-bs-target='#modal'><button class='btn btn-primary' data-bs-toggle='tooltip' data-bs-placement='left' title='Copy Template' onclick='copy(" + i + ")'>\
                    <i class='fa fa-copy'></i>\
                    </button></span>\
                    <button class='btn btn-danger' data-bs-toggle='tooltip' data-bs-placement='left' title='Delete Template' onclick='deleteTemplate(" + i + ")'>\
                    <i class='fa fa-trash-o'></i>\
                    </button></div>"
                    ])
                })
                templateTable.rows.add(templateRows).draw()
                bsInitTooltips()
            } else {
                document.getElementById("emptyMessage").style.display = ""
            }
        }, function () {
            if (loadRequest != latestTemplateLoadRequest) {
                return
            }
            document.getElementById("loading").style.display = "none"
            errorFlash("Error fetching templates")
        })
}

// bindAttachmentUploadReset clears the file input before each new selection,
// so choosing the same file twice still fires a change event. It is bound once
// rather than rebound on every modal open.
var attachmentUploadResetBound = false

function bindAttachmentUploadReset() {
    if (attachmentUploadResetBound) {
        return
    }
    attachmentUploadResetBound = true
    document.getElementById("attachmentUpload").addEventListener("click", function () {
        this.value = null
    })
}

// The application scripts are plain classic scripts at the end of <body>, so
// the document is still parsing when they run and DOMContentLoaded has not
// fired yet.
document.addEventListener('DOMContentLoaded', function () {
    // Modal dismiss handlers (native Bootstrap 5 events, no jQuery bridge)
    document.getElementById('modal').addEventListener('hidden.bs.modal', function (event) {
        dismiss()
    });
    document.getElementById('importEmailModal').addEventListener('show.bs.modal', function () {
        var importRequest = ++latestImportRequest
        enableImportEmailAfterPendingRequest(importRequest)
    })
    document.getElementById('importEmailModal').addEventListener('hidden.bs.modal', function (event) {
        latestImportRequest++
        document.getElementById("email_content").value = ""
        if (!importEmailRequest) {
            setImportEmailFormDisabled(false)
        }
    })
    // Handle Deletion. Bound once here rather than rebound every time the modal
    // opens, and the row is resolved from the clicked icon with a native DOM
    // lookup because the table's row selector no longer goes through jQuery.
    document.getElementById("attachmentsTable").addEventListener("click", function (event) {
        if (saveRequest) {
            return
        }
        var icon = event.target.closest("span > i.fa-trash-o")
        if (!icon || !attachmentsTable) {
            return
        }
        var row = icon.closest("tr")
        if (!row) {
            return
        }
        attachmentsTable.row(row).remove().draw()
    })
    load()

})
