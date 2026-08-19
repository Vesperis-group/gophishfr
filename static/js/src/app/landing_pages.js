/*
	landing_pages.js
	Handles the creation, editing, and deletion of landing pages
	Author: Jordan Wright <github.com/jordan-wright>
*/
var pages = []
var htmlEditor


// Save attempts to POST to /templates/
function save(idx) {
    var page = {}
    page.name = $("#name").val()
    page.html = htmlEditor.getData()
    page.capture_credentials = $("#capture_credentials_checkbox").prop("checked")
    page.capture_passwords = $("#capture_passwords_checkbox").prop("checked")
    page.redirect_url = $("#redirect_url_input").val()
    if (idx != -1) {
        page.id = pages[idx].id
        api.pageId.put(page)
            .done(function (data) {
                successFlash("Page edited successfully!")
                load()
                dismiss()
            })
    } else {
        // Submit the page
        api.pages.post(page)
            .done(function (data) {
                successFlash("Page added successfully!")
                load()
                dismiss()
            })
            .fail(function (data) {
                modalError(data.responseJSON.message)
            })
    }
}

function dismiss() {
    $("#modal\\.flashes").empty()
    $("#name").val("")
    $("#html_editor").val("")
    if (htmlEditor) {
        htmlEditor.setData("")
        htmlEditor.showSource()
    }
    $("#url").val("")
    $("#redirect_url_input").val("")
    $("#modal").find("input[type='checkbox']").prop("checked", false)
    $("#capture_passwords").hide()
    $("#redirect_url").hide()
    bsModalHide("#modal")
}

var deletePage = function (idx) {
    Swal.fire({
        title: "Are you sure?",
        text: "This will delete the landing page. This can't be undone!",
        type: "warning",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Delete " + escapeHtml(pages[idx].name),
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        preConfirm: function () {
            return new Promise(function (resolve, reject) {
                api.pageId.delete(pages[idx].id)
                    .done(function (msg) {
                        resolve()
                    })
                    .fail(function (data) {
                        reject(data.responseJSON.message)
                    })
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
        $('button:contains("OK")').on('click', function () {
            location.reload()
        })
    })
}

function importSite() {
    url = $("#url").val()
    if (!url) {
        modalError("No URL Specified!")
    } else {
        api.clone_site({
                url: url,
                include_resources: false
            })
            .done(function (data) {
                htmlEditor.setData(data.html)
                htmlEditor.showPreview()
                bsModalHide("#importSiteModal")
            })
            .fail(function (data) {
                modalError(data.responseJSON.message)
            })
    }
}

function edit(idx) {
    $("#modalSubmit").unbind('click').click(function () {
        save(idx)
    })
    htmlEditor = GophishHTMLEditor.create("html_editor")
    var page = {}
    if (idx != -1) {
        $("#modalLabel").text("Edit Landing Page")
        page = pages[idx]
        $("#name").val(page.name)
        htmlEditor.setData(page.html)
        $("#capture_credentials_checkbox").prop("checked", page.capture_credentials)
        $("#capture_passwords_checkbox").prop("checked", page.capture_passwords)
        $("#redirect_url_input").val(page.redirect_url)
        if (page.capture_credentials) {
            $("#capture_passwords").show()
            $("#redirect_url").show()
        }
    } else {
        $("#modalLabel").text("New Landing Page")
        htmlEditor.setData("")
    }
    htmlEditor.showSource()
}

function copy(idx) {
    $("#modalSubmit").unbind('click').click(function () {
        save(-1)
    })
    htmlEditor = GophishHTMLEditor.create("html_editor")
    var page = pages[idx]
    $("#name").val("Copy of " + page.name)
    htmlEditor.setData(page.html)
    htmlEditor.showSource()
}

function load() {
    /*
        load() - Loads the current pages using the API
    */
    $("#pagesTable").hide()
    $("#emptyMessage").hide()
    $("#loading").show()
    api.pages.get()
        .done(function (ps) {
            pages = ps
            $("#loading").hide()
            if (pages.length > 0) {
                $("#pagesTable").show()
                pagesTable = $("#pagesTable").DataTable({
                    destroy: true,
                    columnDefs: [{
                        orderable: false,
                        targets: "no-sort"
                    }]
                });
                pagesTable.clear()
                pageRows = []
                $.each(pages, function (i, page) {
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
                $("#emptyMessage").show()
            }
        })
        .fail(function () {
            $("#loading").hide()
            errorFlash("Error fetching pages")
        })
}

$(document).ready(function () {
    // Modal dismiss handler (native Bootstrap 5 event, no jQuery bridge)
    document.getElementById('modal').addEventListener('hidden.bs.modal', function (event) {
        dismiss()
    });
    $("#capture_credentials_checkbox").change(function () {
        $("#capture_passwords").toggle()
        $("#redirect_url").toggle()
    })
    load()
})
