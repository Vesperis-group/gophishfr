/*
	landing_pages.js
	Handles the creation, editing, and deletion of landing pages
	Author: Jordan Wright <github.com/jordan-wright>
*/
var pages = []
var htmlEditor
var latestPageRequest = 0
var latestPageLoadRequest = 0
var saveRequest = null
var activePageId = null
var refreshPageModal = null
var latestImportRequest = 0
var importSiteRequest = null

function setPageFormDisabled(disabled) {
    ["name", "capture_credentials_checkbox", "capture_passwords_checkbox",
        "redirect_url_input"]
        .forEach(function (id) {
            document.getElementById(id).disabled = disabled
        })
    document.getElementById("modalSubmit").disabled = disabled
    document.querySelector('#modal button[onclick^="bsModalShow(\'#importSiteModal\'"]').disabled = disabled
    var codeEditor = document.querySelector("#modal .gophish-code-editor")
    if (codeEditor) {
        codeEditor.inert = disabled
    }
}

function enablePageFormAfterPendingSave(pageRequest) {
    if (!saveRequest) {
        setPageFormDisabled(false)
        return
    }
    var pendingSave = saveRequest
    setPageFormDisabled(true)
    var enableCurrentForm = function () {
        if (pageRequest == latestPageRequest) {
            setPageFormDisabled(false)
        }
    }
    pendingSave.then(enableCurrentForm, enableCurrentForm)
}

function setImportSiteFormDisabled(disabled) {
    document.getElementById("url").disabled = disabled
    document.querySelector("#importSiteModal #modalSubmit").disabled = disabled
}

function enableImportSiteAfterPendingRequest(importRequest) {
    if (!importSiteRequest) {
        setImportSiteFormDisabled(false)
        return
    }
    var pendingImport = importSiteRequest
    setImportSiteFormDisabled(true)
    var enableCurrentForm = function () {
        if (importRequest == latestImportRequest) {
            setImportSiteFormDisabled(false)
        }
    }
    pendingImport.then(enableCurrentForm, enableCurrentForm)
}


// Save attempts to POST or PUT a landing page.
function save(pageId) {
    if (saveRequest) {
        return
    }
    var page = {}
    page.name = document.getElementById("name").value
    page.html = htmlEditor.getData()
    page.capture_credentials = document.getElementById("capture_credentials_checkbox").checked
    page.capture_passwords = document.getElementById("capture_passwords_checkbox").checked
    page.redirect_url = document.getElementById("redirect_url_input").value
    var saveContext = latestPageRequest
    setPageFormDisabled(true)
    if (pageId != -1) {
        page.id = pageId
        saveRequest = api.pageId.put(page)
        saveRequest
            .then(function (data) {
                saveRequest = null
                successFlash("Page edited successfully!")
                load()
                if (saveContext == latestPageRequest) {
                    dismiss()
                } else if (activePageId == page.id && refreshPageModal) {
                    var savedPage = pages.find(function (candidate) {
                        return candidate.id == page.id
                    })
                    if (savedPage) {
                        Object.assign(savedPage, data)
                    }
                    refreshPageModal()
                }
            }, function () {
                saveRequest = null
                if (saveContext == latestPageRequest) {
                    setPageFormDisabled(false)
                }
            })
    } else {
        // Submit the page
        saveRequest = api.pages.post(page)
        saveRequest
            .then(function () {
                saveRequest = null
                successFlash("Page added successfully!")
                load()
                if (saveContext == latestPageRequest) {
                    dismiss()
                }
            }, function (error) {
                saveRequest = null
                if (saveContext == latestPageRequest) {
                    setPageFormDisabled(false)
                    modalError(requestErrorMessage(error))
                }
            })
    }
}

// The previous selector escaped the dot in this id, which defeats jQuery's
// getElementById fast path, so it matched every element carrying the id rather
// than the first.
function clearModalFlashes() {
    document.querySelectorAll('[id="modal.flashes"]').forEach(function (container) {
        container.replaceChildren()
    })
}

// These two blocks are hidden by the stylesheet rather than by an inline
// style, so revealing them needs an explicit display value: clearing the
// inline one would leave the stylesheet rule in force. This is what jQuery's
// show() resolved to for a div.
function setCredentialOptionsVisible(visible) {
    var display = visible ? "block" : "none"
    document.getElementById("capture_passwords").style.display = display
    document.getElementById("redirect_url").style.display = display
}

function dismiss() {
    latestPageRequest++
    activePageId = null
    refreshPageModal = null
    clearModalFlashes()
    document.getElementById("name").value = ""
    document.getElementById("html_editor").value = ""
    if (htmlEditor) {
        htmlEditor.setData("")
        htmlEditor.showSource()
    }
    document.getElementById("url").value = ""
    document.getElementById("redirect_url_input").value = ""
    document.querySelectorAll("#modal input[type='checkbox']").forEach(function (box) {
        box.checked = false
    })
    setCredentialOptionsVisible(false)
    bsModalHide("#modal")
}

var deletePage = function (idx) {
    var pageId = pages[idx].id
    var pageName = pages[idx].name
    var deleteRequest = null
    Swal.fire({
        title: "Are you sure?",
        text: "This will delete the landing page. This can't be undone!",
        type: "warning",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Delete " + escapeHtml(pageName),
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        preConfirm: function () {
            if (deleteRequest) {
                return deleteRequest
            }
            deleteRequest = api.pageId.delete(pageId)
            return deleteRequest.then(function (response) {
                return response
            }, function (error) {
                deleteRequest = null
                Swal.showValidationMessage(requestErrorMessage(error))
            })
        }
    }).then(function (result) {
        if (result.value){
            Swal.fire(
                'Landing Page Deleted!',
                'This landing page has been deleted!',
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

function importSite() {
    if (importSiteRequest) {
        return
    }
    var url = document.getElementById("url").value
    if (!url) {
        modalError("No URL Specified!")
    } else {
        var importContext = latestImportRequest
        setImportSiteFormDisabled(true)
        importSiteRequest = api.clone_site({
            url: url,
            include_resources: false
        })
        importSiteRequest
            .then(function (data) {
                importSiteRequest = null
                if (importContext != latestImportRequest) {
                    return
                }
                htmlEditor.setData(data.html)
                htmlEditor.showPreview()
                bsModalHide("#importSiteModal")
            }, function (error) {
                importSiteRequest = null
                if (importContext != latestImportRequest) {
                    return
                }
                setImportSiteFormDisabled(false)
                modalError(requestErrorMessage(error))
            })
    }
}

function edit(idx) {
    var pageRequest = ++latestPageRequest
    var pageId = idx == -1 ? null : pages[idx].id
    activePageId = pageId
    refreshPageModal = activePageId == null ? null : function () {
        var refreshedIndex = pages.findIndex(function (page) {
            return page.id == activePageId
        })
        if (refreshedIndex != -1) {
            edit(refreshedIndex)
        }
    }
    bindModalSubmit(function () {
        save(pageId == null ? -1 : pageId)
    })
    htmlEditor = GophishHTMLEditor.create("html_editor")
    var page = {}
    if (idx != -1) {
        document.getElementById("modalLabel").textContent = "Edit Landing Page"
        page = pages[idx]
        document.getElementById("name").value = page.name
        htmlEditor.setData(page.html)
        document.getElementById("capture_credentials_checkbox").checked = page.capture_credentials
        document.getElementById("capture_passwords_checkbox").checked = page.capture_passwords
        document.getElementById("redirect_url_input").value = page.redirect_url
        if (page.capture_credentials) {
            setCredentialOptionsVisible(true)
        }
    } else {
        document.getElementById("modalLabel").textContent = "New Landing Page"
        htmlEditor.setData("")
    }
    htmlEditor.showSource()
    enablePageFormAfterPendingSave(pageRequest)
}

function copy(idx) {
    var pageRequest = ++latestPageRequest
    activePageId = null
    refreshPageModal = null
    bindModalSubmit(function () {
        save(-1)
    })
    htmlEditor = GophishHTMLEditor.create("html_editor")
    var page = pages[idx]
    document.getElementById("name").value = "Copy of " + page.name
    htmlEditor.setData(page.html)
    htmlEditor.showSource()
    enablePageFormAfterPendingSave(pageRequest)
}

function load() {
    /*
        load() - Loads the current pages using the API
    */
    var loadRequest = ++latestPageLoadRequest
    document.getElementById("pagesTable").style.display = "none"
    document.getElementById("emptyMessage").style.display = "none"
    document.getElementById("loading").style.display = ""
    api.pages.get()
        .then(function (ps) {
            if (loadRequest != latestPageLoadRequest) {
                return
            }
            pages = ps
            document.getElementById("loading").style.display = "none"
            if (pages.length > 0) {
                document.getElementById("pagesTable").style.display = ""
                pagesTable = new DataTable("#pagesTable", {
                    destroy: true,
                    columnDefs: [{
                        orderable: false,
                        targets: "no-sort"
                    }]
                });
                pagesTable.clear()
                pageRows = []
                pages.forEach(function (page, i) {
                    pageRows.push([
                        escapeHtml(page.name),
                        moment(page.modified_date).format('MMMM Do YYYY, h:mm:ss a'),
                        "<div class='float-end'><span data-bs-toggle='modal' data-bs-target='#modal'><button class='btn btn-primary' data-bs-toggle='tooltip' data-bs-placement='left' title='Edit Page' onclick='edit(" + i + ")'>\
                    <i class='fa fa-pencil'></i>\
                    </button></span>\
		    <span data-bs-toggle='modal' data-bs-target='#modal'><button class='btn btn-primary' data-bs-toggle='tooltip' data-bs-placement='left' title='Copy Page' onclick='copy(" + i + ")'>\
                    <i class='fa fa-copy'></i>\
                    </button></span>\
                    <button class='btn btn-danger' data-bs-toggle='tooltip' data-bs-placement='left' title='Delete Page' onclick='deletePage(" + i + ")'>\
                    <i class='fa fa-trash-o'></i>\
                    </button></div>"
                    ])
                })
                pagesTable.rows.add(pageRows).draw()
                bsInitTooltips()
            } else {
                document.getElementById("emptyMessage").style.display = ""
            }
        }, function () {
            if (loadRequest != latestPageLoadRequest) {
                return
            }
            document.getElementById("loading").style.display = "none"
            errorFlash("Error fetching pages")
        })
}

// submitHandler holds the listener currently bound to the modal's submit
// button, which is reused for every page.
var submitHandler = null

function bindModalSubmit(handler) {
    // Two elements on this page carry the id "modalSubmit", one per modal. The
    // previous selector took jQuery's getElementById fast path and so bound
    // only the first, which is the page modal's Save button.
    var submit = document.getElementById("modalSubmit")
    if (submitHandler) {
        submit.removeEventListener("click", submitHandler)
    }
    submitHandler = handler
    submit.addEventListener("click", submitHandler)
}

// The application scripts are plain classic scripts at the end of <body>, so
// the document is still parsing when they run and DOMContentLoaded has not
// fired yet.
document.addEventListener('DOMContentLoaded', function () {
    // Modal dismiss handler (native Bootstrap 5 event, no jQuery bridge)
    document.getElementById('modal').addEventListener('hidden.bs.modal', function (event) {
        dismiss()
    });
    document.getElementById('importSiteModal').addEventListener('show.bs.modal', function () {
        var importRequest = ++latestImportRequest
        enableImportSiteAfterPendingRequest(importRequest)
    })
    document.getElementById('importSiteModal').addEventListener('hidden.bs.modal', function () {
        latestImportRequest++
        document.getElementById("url").value = ""
        if (!importSiteRequest) {
            setImportSiteFormDisabled(false)
        }
    })
    document.getElementById("capture_credentials_checkbox").addEventListener("change", function () {
        setCredentialOptionsVisible(this.checked)
    })
    load()
})
